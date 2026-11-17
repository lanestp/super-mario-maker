package fileproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
)

// * Never waits on the storage for ever
var emptyUploadClient = &http.Client{Timeout: 30 * time.Second}

// S3 keeps files in a bucket
type S3 struct {
	Client *minio.Client
	Bucket string
}

func (s S3) Put(ctx context.Context, key string, data []byte) error {
	if len(data) == 0 {
		return s.putEmpty(ctx, key)
	}

	_, err := s.Client.PutObject(ctx, s.Bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	})

	return err
}

// * An empty file is sent with a request of its own making. The S3
// * client sends an empty body in a way that leaves out its length,
// * which some S3 servers refuse. A signed link and a plain request
// * that says the length is 0 is taken by all of them
func (s S3) putEmpty(ctx context.Context, key string) error {
	link, err := s.Client.PresignedPutObject(ctx, s.Bucket, key, time.Minute)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPut, link.String(), http.NoBody)
	if err != nil {
		return err
	}

	request.ContentLength = 0

	response, err := emptyUploadClient.Do(request)
	if err != nil {
		// * The link holds a signature, which must not end up in a log
		var urlError *url.Error
		if errors.As(err, &urlError) {
			return fmt.Errorf("storing an empty file: %w", urlError.Err)
		}

		return errors.New("storing an empty file failed")
	}

	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("storing an empty file: the storage answered %d", response.StatusCode)
	}

	return nil
}

func (s S3) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	object, err := s.Client.GetObject(ctx, s.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}

	// * Nothing is asked of the bucket until the object is first used
	info, err := object.Stat()
	if err != nil {
		object.Close()

		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, 0, ErrNotFound
		}

		return nil, 0, err
	}

	return object, info.Size, nil
}
