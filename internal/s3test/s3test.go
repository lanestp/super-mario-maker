// Package s3test runs a fake S3 server inside a test, so tests need no
// real S3 server. It accepts the presigned uploads and downloads the
// server hands to consoles, but does not check signatures or upload
// policies, so it can not be used to test that those are enforced
package s3test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	AccessKey = "test-access-key"
	SecretKey = "test-secret-key"
	Bucket    = "smm"
)

// Server is a running fake S3 server with one empty bucket, named Bucket
type Server struct {
	// Endpoint is the host and port of the server, which is served over plain HTTP
	Endpoint string
	Client   *minio.Client
}

// Start starts a fake S3 server, which is stopped when the test finishes
func Start(t *testing.T) *Server {
	t.Helper()

	httpServer := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(httpServer.Close)

	endpoint := strings.TrimPrefix(httpServer.URL, "http://")

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(AccessKey, SecretKey, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("creating S3 client: %v", err)
	}

	if err := client.MakeBucket(context.Background(), Bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("creating bucket: %v", err)
	}

	return &Server{Endpoint: endpoint, Client: client}
}

// Put stores an object in the bucket
func (s *Server) Put(t *testing.T, key string, data []byte) {
	t.Helper()

	_, err := s.Client.PutObject(context.Background(), Bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{})
	if err != nil {
		t.Fatalf("storing %s: %v", key, err)
	}
}
