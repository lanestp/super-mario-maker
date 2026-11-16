// Command smm-local-s3 is a stand-in S3 server for trying the game server
// out on one machine. It keeps files in a directory, and makes sure the
// bucket and the event course metadata file the game server needs exist.
//
// It is for local testing ONLY. It does not check access keys, signatures
// or upload policies, so anyone who can reach it can read and write
// everything in it. Never run it where other machines can reach it
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3afero"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/spf13/afero"
)

// * The event course metadata file the game server refuses to start without
const eventCourseMetadataKey = "900000.bin"

func main() {
	address := flag.String("addr", "127.0.0.1:9000", "address to listen on. Only addresses on this machine are allowed")
	directory := flag.String("dir", "local-data/s3", "directory to keep files in")
	bucket := flag.String("bucket", "smm", "bucket to make sure exists")
	flag.Parse()

	if err := run(*address, *directory, *bucket); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(address, directory, bucket string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}

	// * There is no access control at all, so it must not be reachable from other machines
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("refusing to listen on " + address + ", as this server has no access control. Use 127.0.0.1")
	}

	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}

	backend, err := s3afero.MultiBucket(afero.NewBasePathFs(afero.NewOsFs(), directory))
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}

	server := &http.Server{
		Handler:           gofakes3.New(backend).Server(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveError := make(chan error, 1)
	go func() { serveError <- server.Serve(listener) }()

	// * The bucket and file are made through the server itself, so
	// * that they are stored the way it expects
	if err := prepare(listener.Addr().String(), bucket); err != nil {
		return err
	}

	fmt.Printf("Local S3 stand-in listening on %s, keeping files in %s. For local testing only\n", address, directory)

	return <-serveError
}

func prepare(address, bucket string) error {
	client, err := minio.New(address, &minio.Options{
		Creds:  credentials.NewStaticV4("local", "local-secret", ""),
		Secure: false,
	})
	if err != nil {
		return err
	}

	ctx := context.Background()

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}

	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return err
		}
	}

	// * The game server only needs the file to exist to start. This is a
	// * placeholder and not real event course data, and it holds one
	// * byte as this server does not accept an empty upload
	if _, err := client.StatObject(ctx, bucket, eventCourseMetadataKey, minio.StatObjectOptions{}); err != nil {
		placeholder := []byte{0}

		_, err := client.PutObject(ctx, bucket, eventCourseMetadataKey, bytes.NewReader(placeholder), int64(len(placeholder)), minio.PutObjectOptions{})
		if err != nil {
			return err
		}

		fmt.Println("Created a placeholder", eventCourseMetadataKey)
	}

	return nil
}
