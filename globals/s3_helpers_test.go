package globals

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/PretendoNetwork/super-mario-maker/internal/s3test"
	"github.com/minio/minio-go/v7"
)

func clearS3Settings(t *testing.T) {
	t.Helper()

	for _, setting := range []string{"ENDPOINT", "ACCESS_KEY", "ACCESS_SECRET", "SECURE", "REGION"} {
		t.Setenv("PN_SMM_CONFIG_S3_"+setting, "")
	}
}

func TestS3IsReachedOverTLSUnlessToldOtherwise(t *testing.T) {
	cases := []struct {
		secure       string
		wantScheme   string
		wantWasGiven bool
	}{
		{"", "https", false},
		{"maybe", "https", false},
		{"true", "https", true},
		{"false", "http", true},
		{"0", "http", true},
	}

	for _, c := range cases {
		clearS3Settings(t)
		t.Setenv("PN_SMM_CONFIG_S3_ENDPOINT", "s3.example.com")
		t.Setenv("PN_SMM_CONFIG_S3_SECURE", c.secure)

		client, wasGiven, err := S3ClientFromEnv()
		if err != nil {
			t.Fatalf("PN_SMM_CONFIG_S3_SECURE=%q: %v", c.secure, err)
		}

		if client.EndpointURL().Scheme != c.wantScheme || client.EndpointURL().Host != "s3.example.com" {
			t.Errorf("PN_SMM_CONFIG_S3_SECURE=%q: reaches S3 at %s, want %s://s3.example.com", c.secure, client.EndpointURL(), c.wantScheme)
		}

		if wasGiven != c.wantWasGiven {
			t.Errorf("PN_SMM_CONFIG_S3_SECURE=%q: reported as given %v, want %v", c.secure, wasGiven, c.wantWasGiven)
		}
	}
}

func TestAnS3EndpointWithAPathIsRefused(t *testing.T) {
	clearS3Settings(t)
	t.Setenv("PN_SMM_CONFIG_S3_ENDPOINT", "s3.example.com/smm")

	if _, _, err := S3ClientFromEnv(); err == nil {
		t.Error("an endpoint with a path was taken")
	}
}

// * Points MinIOClient at an S3 server in memory with the bucket smm. The
// * region is given so that making links does not have to look it up
func fakeS3(t *testing.T) (*s3test.Server, *minio.Client) {
	t.Helper()

	server := s3test.Start(t)

	clearS3Settings(t)
	t.Setenv("PN_SMM_CONFIG_S3_ENDPOINT", server.Endpoint)
	t.Setenv("PN_SMM_CONFIG_S3_ACCESS_KEY", s3test.AccessKey)
	t.Setenv("PN_SMM_CONFIG_S3_ACCESS_SECRET", s3test.SecretKey)
	t.Setenv("PN_SMM_CONFIG_S3_SECURE", "false")
	t.Setenv("PN_SMM_CONFIG_S3_REGION", "us-east-1")

	client, _, err := S3ClientFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	previous := MinIOClient
	MinIOClient = client
	t.Cleanup(func() { MinIOClient = previous })

	return server, client
}

func TestTheSizeOfAFileInS3(t *testing.T) {
	server, _ := fakeS3(t)

	server.Put(t, "courses/940000.bin", bytes.Repeat([]byte{1}, 300))

	size, err := S3ObjectSize(s3test.Bucket, "courses/940000.bin")
	if err != nil || size != 300 {
		t.Errorf("got size %d (%v), want 300", size, err)
	}

	if _, err := S3ObjectSize(s3test.Bucket, "courses/940001.bin"); err == nil {
		t.Error("a file that is not there has a size")
	}

	if info, err := S3StatObject(s3test.Bucket, "courses/940000.bin"); err != nil || info.Key != "courses/940000.bin" {
		t.Errorf("got %+v (%v), want the file's details", info, err)
	}
}

func TestADownloadLinkFromS3IsSignedAndLastsAsLongAsAsked(t *testing.T) {
	_, client := fakeS3(t)

	link, err := NewS3Presigner(client).GetObject(s3test.Bucket, "courses/940000.bin", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	query := link.Query()

	if link.Host != client.EndpointURL().Host || link.Path != "/smm/courses/940000.bin" {
		t.Errorf("the link goes to %s, want the file in the bucket", link)
	}

	if query.Get("X-Amz-Expires") != "900" || query.Get("X-Amz-Signature") == "" || !strings.HasPrefix(query.Get("X-Amz-Credential"), s3test.AccessKey+"/") {
		t.Errorf("the link is not signed by the access key for 15 minutes: %s", link)
	}
}

func TestAnUploadFormForS3OnlyTakesTheOneFile(t *testing.T) {
	_, client := fakeS3(t)

	link, fields, err := NewS3Presigner(client).PostObject(s3test.Bucket, "courses/940000.bin", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if link.Host != client.EndpointURL().Host || strings.TrimSuffix(link.Path, "/") != "/smm" {
		t.Errorf("the form is sent to %s, want the bucket", link)
	}

	if fields["key"] != "courses/940000.bin" || fields["policy"] == "" || fields["x-amz-signature"] == "" {
		t.Errorf("got form fields %v, want the key, a policy and its signature", fields)
	}
}
