package accountapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// * The port without TLS must hand out files and nothing else. Logging
// * in there would send a password hash where anyone can read it
func TestThePlainPortServesFilesAndNothingElse(t *testing.T) {
	files := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "file handler saw "+r.URL.Path)
	})

	plain := httptest.NewServer(Server{Files: files, FilesPath: "/v1/files"}.PlainFilesHandler())
	t.Cleanup(plain.Close)

	get := func(path string) (int, string) {
		response, err := http.Get(plain.URL + path)
		if err != nil {
			t.Fatal(err)
		}

		defer response.Body.Close()

		body, _ := io.ReadAll(response.Body)

		return response.StatusCode, string(body)
	}

	for _, path := range []string{"/v1/files", "/v1/files/courses/1.bin"} {
		if status, body := get(path); status != http.StatusOK || body != "file handler saw "+path {
			t.Errorf("%s answered %d %q, want the file handler", path, status, body)
		}
	}

	for _, path := range []string{"/", "/v1/api/provider/nex_token/@me", "/v1/api/admin/mapped_ids", "/v1/filesystem"} {
		if status, body := get(path); status != http.StatusNotFound {
			t.Errorf("%s answered %d %q on the plain port, want 404", path, status, body)
		}
	}

	response, err := http.Post(plain.URL+"/v1/api/oauth20/access_token/generate", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}

	response.Body.Close()

	if response.StatusCode != http.StatusNotFound {
		t.Errorf("logging in on the plain port answered %d, want 404", response.StatusCode)
	}
}
