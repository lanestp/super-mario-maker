package fileproxy_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/PretendoNetwork/super-mario-maker/fileproxy"
	"github.com/PretendoNetwork/super-mario-maker/internal/s3test"
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
