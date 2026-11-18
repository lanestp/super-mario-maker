package fileproxy_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PretendoNetwork/super-mario-maker/fileproxy"
	"github.com/PretendoNetwork/super-mario-maker/internal/s3test"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestS3KeepsAndReturnsFiles(t *testing.T) {
	server := s3test.Start(t)
	storage := fileproxy.S3{Client: server.Client, Bucket: s3test.Bucket}

	for name, data := range map[string][]byte{
		"courses/1.bin": []byte("a course file \x00\xff"),
		// * A game uploads an empty file for its maker object
		"courses/2.bin": {},
	} {
		if err := storage.Put(context.Background(), name, data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		file, size, err := storage.Get(context.Background(), name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		got, err := io.ReadAll(file)
		file.Close()

		if err != nil || !bytes.Equal(got, data) || size != int64(len(data)) {
			t.Errorf("%s: read back %q with size %d (%v), want %q", name, got, size, err, data)
		}
	}

	if _, _, err := storage.Get(context.Background(), "courses/missing.bin"); !errors.Is(err, fileproxy.ErrNotFound) {
		t.Errorf("a file that is not there: got %v, want ErrNotFound", err)
	}
}

// * An empty file is sent with a request of its own, and any 2xx
// * answer to it means it was stored
func TestAnEmptyFileIsStoredOnlyWhenTheStorageAnswersWithSuccess(t *testing.T) {
	var answer atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(answer.Load()))
	}))
	t.Cleanup(server.Close)

	client, err := minio.New(strings.TrimPrefix(server.URL, "http://"), &minio.Options{
		Creds:  credentials.NewStaticV4(s3test.AccessKey, s3test.SecretKey, ""),
		Region: "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	storage := fileproxy.S3{Client: client, Bucket: s3test.Bucket}

	for status, stored := range map[int]bool{200: true, 204: true, 299: true, 300: false, 404: false, 500: false} {
		answer.Store(int32(status))

		if err := storage.Put(context.Background(), "courses/1.bin", nil); (err == nil) != stored {
			t.Errorf("an answer of %d gave %v, want it taken as stored: %v", status, err, stored)
		}
	}
}
