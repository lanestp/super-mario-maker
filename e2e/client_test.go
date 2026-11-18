package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/super-mario-maker/accounts"
	"github.com/PretendoNetwork/super-mario-maker/internal/s3test"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
	"github.com/minio/minio-go/v7"
)

// * What e2e/client/smm_client.py prints
type clientResult struct {
	PID                     uint32 `json:"pid"`
	ApplicationConfigLength int    `json:"application_config_length"`
	DataID                  uint64 `json:"data_id"`
	UploadURL               string `json:"upload_url"`
	UploadStatus            int    `json:"upload_status"`
	Meta                    struct {
		DataID     uint64   `json:"data_id"`
		OwnerID    uint64   `json:"owner_id"`
		Size       int      `json:"size"`
		Name       string   `json:"name"`
		DataType   int      `json:"data_type"`
		MetaBinary string   `json:"meta_binary"`
		Flag       int      `json:"flag"`
		Period     int      `json:"period"`
		Tags       []string `json:"tags"`
		Ratings    int      `json:"ratings"`
	} `json:"meta"`
	DownloadURL     string `json:"download_url"`
	DownloadSize    int    `json:"download_size"`
	DownloadMatches bool   `json:"download_matches"`
	RatingTotal     int    `json:"rating_total"`
	RatingCount     int    `json:"rating_count"`
}

// * Runs a separate implementation of the game protocol against the
// * running server: it logs in, uploads a course, reads it back and
// * rates it, as a console would.
// *
// * Skipped unless PN_SMM_E2E_PYTHON is set to a Python 3.12+ which
// * has the packages in e2e/client/requirements.txt installed
func TestConsoleCanUploadAndDownloadACourse(t *testing.T) {
	python := os.Getenv("PN_SMM_E2E_PYTHON")
	if python == "" {
		t.Skip("PN_SMM_E2E_PYTHON not set, skipping game protocol client test")
	}

	// * The tests run in e2e/, so a relative path such as the one the
	// * README gives is taken from the repository root
	if !filepath.IsAbs(python) && strings.Contains(python, "/") {
		python = filepath.Join("..", python)
	}

	s3 := s3test.Start(t)
	s3.Put(t, "courses/900000.bin", []byte("event course metadata"))

	var account store.Account

	started := startServerWith(t, s3, true, func(database *sqlite.Store) {
		var err error

		account, err = accounts.Create(database, "PlayerOne", "secret123", "")
		if err != nil {
			t.Fatal(err)
		}
	})

	started.waitUntilReady(t)

	client := exec.Command(python, "client/smm_client.py")
	client.Env = append(os.Environ(),
		"SMM_HOST=127.0.0.1",
		"SMM_PORT="+strconv.Itoa(started.AuthenticationPort),
		"SMM_PID="+strconv.Itoa(int(account.PID)),
		"SMM_NEX_PASSWORD="+account.NEXPassword,
	)

	var stderr bytes.Buffer
	client.Stderr = &stderr

	output, err := client.Output()
	if err != nil {
		t.Fatalf("the client failed: %v\n%s\nserver output:\n%s", err, stderr.String(), started.output.String())
	}

	var result clientResult
	if err := json.Unmarshal(bytes.TrimSpace(output), &result); err != nil {
		t.Fatalf("the client printed %q: %v", output, err)
	}

	// * What the client was told
	if result.DataID != store.FirstDataID {
		t.Errorf("the course got DataID %d, want %d", result.DataID, store.FirstDataID)
	}

	if result.UploadStatus < 200 || result.UploadStatus > 299 {
		t.Errorf("the upload got status %d", result.UploadStatus)
	}

	// * The client must only ever be sent to the server itself for
	// * files, never to the S3 server behind it
	server := "http://127.0.0.1:" + strconv.Itoa(started.AccountAPIPort) + "/v1/files"

	if !strings.HasPrefix(result.UploadURL, server) || !strings.HasPrefix(result.DownloadURL, server) {
		t.Errorf("the client was not sent to the server for files: upload %s, download %s", result.UploadURL, result.DownloadURL)
	}

	if strings.Contains(result.UploadURL, s3.Endpoint) || strings.Contains(result.DownloadURL, s3.Endpoint) {
		t.Errorf("the client was sent to the S3 server: upload %s, download %s", result.UploadURL, result.DownloadURL)
	}

	meta := result.Meta
	if meta.DataID != result.DataID || meta.OwnerID != uint64(account.PID) || meta.Size != 4096 || meta.Name != "E2E Course" || meta.DataType != 6 {
		t.Errorf("the client read back %+v", meta)
	}

	if meta.MetaBinary != "01020304" || meta.Flag != 256 || meta.Period != 90 || len(meta.Tags) != 1 || meta.Tags[0] != "e2e" || meta.Ratings != 2 {
		t.Errorf("the client read back %+v", meta)
	}

	if !result.DownloadMatches || result.DownloadSize != 4096 {
		t.Errorf("the downloaded course did not match the uploaded one (size %d)", result.DownloadSize)
	}

	if result.RatingTotal != 10 || result.RatingCount != 2 {
		t.Errorf("after rating 5 twice the client was told total %d and count %d, want 10 and 2", result.RatingTotal, result.RatingCount)
	}

	if result.ApplicationConfigLength == 0 {
		t.Error("the client got an empty application config")
	}

	// * What the server kept. The database is read from this process
	database, err := sqlite.Open(started.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}

	defer database.Close()

	info, nexError := database.GetObjectInfoByDataID(types.UInt64(result.DataID))
	if nexError != nil {
		t.Fatalf("the course is not available in the database: %v", nexError)
	}

	if uint32(info.OwnerID) != account.PID || info.Name != "E2E Course" || info.Size != 4096 {
		t.Errorf("the database has owner %d, name %q, size %d", info.OwnerID, info.Name, info.Size)
	}

	// * And the file itself must be in S3, whole
	var stored []byte

	for object := range s3.Client.ListObjects(context.Background(), s3test.Bucket, minio.ListObjectsOptions{Recursive: true}) {
		if object.Err != nil {
			t.Fatal(object.Err)
		}

		if object.Key == "courses/900000.bin" {
			continue
		}

		reader, err := s3.Client.GetObject(context.Background(), s3test.Bucket, object.Key, minio.GetObjectOptions{})
		if err != nil {
			t.Fatal(err)
		}

		stored, err = io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("course stored in S3 as %s", object.Key)
	}

	want := bytes.Repeat(func() []byte {
		pattern := make([]byte, 256)
		for i := range pattern {
			pattern[i] = byte(i)
		}

		return pattern
	}(), 16)

	if !bytes.Equal(stored, want) {
		t.Errorf("S3 holds %d bytes for the course, which is not what was uploaded", len(stored))
	}
}
