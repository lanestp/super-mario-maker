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
	"io"
	"mime"
	"mime/multipart"
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

// * The event course metadata file the game server refuses to start
// * without. It is kept under the same key prefix as course files
const eventCourseMetadataName = "900000.bin"

func main() {
	address := flag.String("addr", "127.0.0.1:9000", "address to listen on. Only addresses on this machine are allowed")
	directory := flag.String("dir", "local-data/s3", "directory to keep files in")
	bucket := flag.String("bucket", "smm", "bucket to make sure exists")
	keyBase := flag.String("key-base", "courses", "key prefix the game server keeps files under (its PN_SMM_CONFIG_S3_KEY_BASE)")
	flag.Parse()

	if err := run(*address, *directory, *bucket, *keyBase+"/"+eventCourseMetadataName); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(address, directory, bucket, eventCourseMetadataKey string) error {
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
		Handler:           logRequests(nameUploadedFiles(gofakes3.New(backend).Server())),
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveError := make(chan error, 1)
	go func() { serveError <- server.Serve(listener) }()

	// * The bucket and file are made through the server itself, so
	// * that they are stored the way it expects
	if err := prepare(listener.Addr().String(), bucket, eventCourseMetadataKey); err != nil {
		return err
	}

	fmt.Printf("Local S3 stand-in listening on %s, keeping files in %s. For local testing only\n", address, directory)

	return <-serveError
}

func prepare(address, bucket, eventCourseMetadataKey string) error {
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

type statusRecorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if r.body.Len() < maxLoggedBytes {
		r.body.Write(data)
	}

	return r.ResponseWriter.Write(data)
}

// * How much of a refused request and its answer is printed
const maxLoggedBytes = 4096

// * Prints each request, so that it can be seen whether a console's
// * uploads and downloads arrive. Query strings are left out, as
// * presigned URLs carry their signature there. A refused request is
// * printed with its headers, the start of what was sent and the
// * answer, as a console gives no detail about why an upload failed
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		var sent bytes.Buffer
		r.Body = io.NopCloser(io.TeeReader(r.Body, &limitedWriter{buffer: &sent, remaining: maxLoggedBytes}))

		next.ServeHTTP(recorder, r)

		fmt.Printf("[S3] %s %s %s -> %d (%d bytes sent by client)\n", time.Now().Format("15:04:05"), r.Method, r.URL.Path, recorder.status, r.ContentLength)

		if recorder.status >= http.StatusBadRequest && r.Method != http.MethodHead {
			fmt.Printf("[S3] refused request headers: %q\n[S3] refused request start: %q\n[S3] answer: %q\n", r.Header, sent.String(), recorder.body.String())
		}
	})
}

type limitedWriter struct {
	buffer    *bytes.Buffer
	remaining int
}

func (w *limitedWriter) Write(data []byte) (int, error) {
	kept := min(len(data), w.remaining)
	w.buffer.Write(data[:kept])
	w.remaining -= kept

	return len(data), nil
}

// * The largest form upload that is rewritten. Course files are far smaller
const maxFormUploadBytes = 64 << 20

// * A console sends the "file" part of an upload form without a file
// * name. S3 accepts that, but the stand-in only sees a part as a file
// * when it has a name, and refuses the upload. This gives the part a
// * name before the stand-in reads the form
func nameUploadedFiles(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if r.Method != http.MethodPost || err != nil || mediaType != "multipart/form-data" {
			next.ServeHTTP(w, r)
			return
		}

		body, contentType, err := renameFilePart(http.MaxBytesReader(w, r.Body, maxFormUploadBytes), params["boundary"])
		if err != nil {
			http.Error(w, "could not read the upload form: "+err.Error(), http.StatusBadRequest)
			return
		}

		r.Body = io.NopCloser(body)
		r.ContentLength = int64(body.Len())
		r.Header.Set("Content-Type", contentType)
		r.Header.Set("Content-Length", fmt.Sprint(body.Len()))

		next.ServeHTTP(w, r)
	})
}

// * Copies a form, giving the "file" part a file name if it has none
func renameFilePart(form io.Reader, boundary string) (*bytes.Buffer, string, error) {
	reader := multipart.NewReader(form, boundary)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, "", err
		}

		var copied io.Writer

		if part.FormName() == "file" && part.FileName() == "" {
			copied, err = writer.CreateFormFile("file", "upload")
		} else {
			copied, err = writer.CreatePart(part.Header)
		}

		if err != nil {
			return nil, "", err
		}

		if _, err := io.Copy(copied, part); err != nil {
			return nil, "", err
		}
	}

	if err := writer.Close(); err != nil {
		return nil, "", err
	}

	return body, writer.FormDataContentType(), nil
}
