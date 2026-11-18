package accountapi_test

import (
	"net/http"
	"strings"
	"testing"
)

// * Cemu accepts a Miiverse answer only with has_error 0 and version 1,
// * and reads the endpoint's host from it (Cemu's nn_olv)
func TestMiiverseSaysWhereItIsSoTheGameCanStartIt(t *testing.T) {
	f := newFixture(t)
	host := strings.TrimPrefix(f.server.URL, "http://")

	for _, path := range []string{"/olv/v1/endpoint", "/unserved/olv"} {
		result := f.get(path, "")

		if result.status != http.StatusOK || result.root(t) != "result" {
			t.Fatalf("%s: got status %d and body %s", path, result.status, result.body)
		}

		if hasError, version := result.field(t, "has_error"), result.field(t, "version"); hasError != "0" || version != "1" {
			t.Errorf("%s: has_error %q and version %q, want 0 and 1", path, hasError, version)
		}

		for _, name := range []string{"host", "api_host", "portal_host", "n3ds_host"} {
			if got := result.field(t, name); got != host+"/olv" {
				t.Errorf("%s: %s is %q, want %q", path, name, got, host+"/olv")
			}
		}
	}
}

func TestMiiverseAnswersAnythingElseWithAnEmptySuccess(t *testing.T) {
	f := newFixture(t)

	for _, path := range []string{"/olv/v1/posts.search?by=self", "/olv/v1/communities/0/posts"} {
		result := f.get(path, "")

		if result.status != http.StatusOK || result.field(t, "has_error") != "0" || result.field(t, "version") != "1" {
			t.Errorf("%s: got status %d and body %s", path, result.status, result.body)
		}
	}
}
