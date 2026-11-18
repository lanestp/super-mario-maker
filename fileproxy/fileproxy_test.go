package fileproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type memory struct {
	files   map[string][]byte
	failing bool
}

func (m *memory) Put(_ context.Context, key string, data []byte) error {
	if m.failing {
		return errors.New("storage is down: secret detail")
	}

	m.files[key] = data

	return nil
}

func (m *memory) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	if m.failing {
		return nil, 0, errors.New("storage is down: secret detail")
	}

	data, found := m.files[key]
	if !found {
		return nil, 0, ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

type setup struct {
	proxy   *Proxy
	storage *memory
	server  *httptest.Server
	now     time.Time
}

func newSetup(t *testing.T) *setup {
	t.Helper()

	s := &setup{storage: &memory{files: map[string][]byte{}}, now: time.Unix(1_800_000_000, 0)}
	s.proxy = &Proxy{
		Secret:         []byte("a secret only the server knows"),
		Storage:        s.storage,
		MaxUploadBytes: 1000,
		Now:            func() time.Time { return s.now },
	}

	mux := http.NewServeMux()
	mux.Handle(Path, s.proxy.Handler())
	mux.Handle(Path+"/", s.proxy.Handler())

	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)

	base, err := url.Parse(s.server.URL)
	if err != nil {
		t.Fatal(err)
	}

	s.proxy.BaseURL = base

	return s
}

// * Builds the form the way a console does: the fields, then the file
// * part with no file name
func consoleForm(fields map[string]string, file []byte) (string, []byte) {
	const boundary = "--------BOUNDARY--------45143351D5E385E3"

	var body bytes.Buffer

	for name, value := range fields {
		body.WriteString("--" + boundary + "\r\nContent-Disposition: form-data; name=\"" + name + "\"\r\n\r\n" + value + "\r\n")
	}

	if file != nil {
		body.WriteString("--" + boundary + "\r\nContent-Disposition: form-data; name=\"file\"\r\n\r\n")
		body.Write(file)
		body.WriteString("\r\n")
	}

	body.WriteString("--" + boundary + "--\r\n")

	return "multipart/form-data; boundary=" + boundary, body.Bytes()
}

func (s *setup) upload(t *testing.T, fields map[string]string, file []byte) (int, string) {
	t.Helper()

	link, _, err := s.proxy.PostObject("", "unused", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	contentType, body := consoleForm(fields, file)

	response, err := http.Post(link.String(), contentType, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	defer response.Body.Close()

	answer, _ := io.ReadAll(response.Body)

	return response.StatusCode, string(answer)
}

func get(t *testing.T, link string) (int, []byte, http.Header) {
	t.Helper()

	response, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}

	defer response.Body.Close()

	body, _ := io.ReadAll(response.Body)

	return response.StatusCode, body, response.Header
}

func TestAFileUploadedThroughALinkCanBeDownloadedThroughALink(t *testing.T) {
	s := newSetup(t)

	link, fields, err := s.proxy.PostObject("bucket", "courses/940001.bin", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(link.String(), s.server.URL) {
		t.Errorf("the upload link is %s, which is not on this server", link)
	}

	file := []byte("ASH0 course \x00\xff data")

	if status, answer := s.upload(t, fields, file); status != http.StatusNoContent {
		t.Fatalf("upload answered %d: %s", status, answer)
	}

	if !bytes.Equal(s.storage.files["courses/940001.bin"], file) {
		t.Fatalf("stored %q under the key, want the file", s.storage.files["courses/940001.bin"])
	}

	download, err := s.proxy.GetObject("bucket", "courses/940001.bin", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	status, body, header := get(t, download.String())
	if status != http.StatusOK || !bytes.Equal(body, file) {
		t.Errorf("download answered %d with %q, want the file", status, body)
	}

	if header.Get("Content-Length") != strconv.Itoa(len(file)) || header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("download headers: length %q, sniffing %q", header.Get("Content-Length"), header.Get("X-Content-Type-Options"))
	}
}

// * A game uploads an empty file for its maker object
func TestAnEmptyFileCanBeUploaded(t *testing.T) {
	s := newSetup(t)

	_, fields, _ := s.proxy.PostObject("", "courses/940002.bin", time.Minute)

	if status, answer := s.upload(t, fields, []byte{}); status != http.StatusNoContent {
		t.Fatalf("upload answered %d: %s", status, answer)
	}

	if data, stored := s.storage.files["courses/940002.bin"]; !stored || len(data) != 0 {
		t.Errorf("stored %v (%v), want an empty file", data, stored)
	}
}

func TestUploadsThatWereNotHandedALinkAreRefused(t *testing.T) {
	s := newSetup(t)

	_, good, _ := s.proxy.PostObject("", "courses/1.bin", time.Minute)
	_, other, _ := s.proxy.PostObject("", "courses/2.bin", time.Minute)

	change := func(name, value string) map[string]string {
		fields := map[string]string{}
		for k, v := range good {
			fields[k] = v
		}

		fields[name] = value

		return fields
	}

	without := func(name string) map[string]string {
		fields := change(name, "")
		delete(fields, name)

		return fields
	}

	download, _ := s.proxy.GetObject("", "courses/1.bin", time.Minute)

	for name, fields := range map[string]map[string]string{
		"another file's key":             change("key", "courses/2.bin"),
		"another file's signature":       change("signature", other["signature"]),
		"a later expiry":                 change("expires", "9999999999"),
		"a download link's signature":    change("signature", download.Query().Get("signature")),
		"no signature":                   without("signature"),
		"no expiry":                      without("expires"),
		"no key":                         without("key"),
		"an expiry that is not a number": change("expires", "soon"),
	} {
		if status, _ := s.upload(t, fields, []byte("data")); status != http.StatusForbidden {
			t.Errorf("%s: answered %d, want 403", name, status)
		}
	}

	if len(s.storage.files) != 0 {
		t.Errorf("something was stored: %v", s.storage.files)
	}

	// * The untouched link still works, so the refusals above were for the changes
	if status, _ := s.upload(t, good, []byte("data")); status != http.StatusNoContent {
		t.Errorf("the good link answered %d", status)
	}
}

func TestLinksStopWorkingWhenTheirTimeIsUp(t *testing.T) {
	s := newSetup(t)
	s.storage.files["courses/1.bin"] = []byte("data")

	_, fields, _ := s.proxy.PostObject("", "courses/1.bin", time.Minute)
	download, _ := s.proxy.GetObject("", "courses/1.bin", time.Minute)

	s.now = s.now.Add(time.Minute)

	if status, _, _ := get(t, download.String()); status != http.StatusOK {
		t.Errorf("download at the last second answered %d, want 200", status)
	}

	s.now = s.now.Add(time.Second)

	if status, _, _ := get(t, download.String()); status != http.StatusForbidden {
		t.Errorf("download after its time answered %d, want 403", status)
	}

	if status, _ := s.upload(t, fields, []byte("late")); status != http.StatusForbidden {
		t.Errorf("upload after its time answered %d, want 403", status)
	}
}

func TestDownloadsThatWereNotHandedALinkAreRefused(t *testing.T) {
	s := newSetup(t)
	s.storage.files["courses/1.bin"] = []byte("one")
	s.storage.files["courses/2.bin"] = []byte("two")

	good, _ := s.proxy.GetObject("", "courses/1.bin", time.Minute)
	_, upload, _ := s.proxy.PostObject("", "courses/1.bin", time.Minute)

	withQuery := func(change func(url.Values)) string {
		link := *good
		query := link.Query()
		change(query)
		link.RawQuery = query.Encode()

		return link.String()
	}

	otherFile := *good
	otherFile.Path = strings.Replace(otherFile.Path, "1.bin", "2.bin", 1)

	for name, link := range map[string]string{
		"another file":               otherFile.String(),
		"no signature":               withQuery(func(q url.Values) { q.Del("signature") }),
		"a later expiry":             withQuery(func(q url.Values) { q.Set("expires", "9999999999") }),
		"an upload link's signature": withQuery(func(q url.Values) { q.Set("signature", upload["signature"]) }),
		"no query at all":            s.server.URL + Path + "/courses/1.bin",
	} {
		if status, body, _ := get(t, link); status != http.StatusForbidden || bytes.Contains(body, []byte("one")) || bytes.Contains(body, []byte("two")) {
			t.Errorf("%s: answered %d with %q, want 403 and no file", name, status, body)
		}
	}

	// * A proxy with another secret makes links this one refuses
	stranger := &Proxy{BaseURL: s.proxy.BaseURL, Secret: []byte("another secret"), Now: s.proxy.Now}
	forged, _ := stranger.GetObject("", "courses/1.bin", time.Minute)

	if status, _, _ := get(t, forged.String()); status != http.StatusForbidden {
		t.Errorf("a link signed with another secret answered %d, want 403", status)
	}
}

func TestUploadsThatAreTooLargeOrMalformedAreRefused(t *testing.T) {
	s := newSetup(t)

	_, fields, _ := s.proxy.PostObject("", "courses/1.bin", time.Minute)

	if status, _ := s.upload(t, fields, make([]byte, 1000)); status != http.StatusNoContent {
		t.Errorf("a file of exactly the limit answered %d, want 204", status)
	}

	delete(s.storage.files, "courses/1.bin")

	if status, _ := s.upload(t, fields, make([]byte, 1001)); status != http.StatusRequestEntityTooLarge {
		t.Errorf("a file one byte over the limit answered %d, want 413", status)
	}

	if status, _ := s.upload(t, fields, make([]byte, 500_000)); status != http.StatusRequestEntityTooLarge && status != http.StatusBadRequest {
		t.Errorf("a huge file answered %d, want it refused", status)
	}

	if status, _ := s.upload(t, fields, nil); status != http.StatusBadRequest {
		t.Errorf("a form with no file answered %d, want 400", status)
	}

	response, err := http.Post(s.server.URL+Path, "application/json", strings.NewReader(`{"key":"courses/1.bin"}`))
	if err != nil {
		t.Fatal(err)
	}

	response.Body.Close()

	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("a body that is not a form answered %d, want 400", response.StatusCode)
	}

	if len(s.storage.files) != 0 {
		t.Errorf("something was stored: %d files", len(s.storage.files))
	}
}

func TestMissingFilesAndStorageFailuresDoNotLeakDetails(t *testing.T) {
	s := newSetup(t)

	var logged []error
	s.proxy.LogError = func(err error) { logged = append(logged, err) }

	missing, _ := s.proxy.GetObject("", "courses/404.bin", time.Minute)

	if status, _, _ := get(t, missing.String()); status != http.StatusNotFound {
		t.Errorf("a file that is not there answered %d, want 404", status)
	}

	s.storage.failing = true

	status, body, _ := get(t, missing.String())
	if status != http.StatusBadGateway || bytes.Contains(body, []byte("secret detail")) {
		t.Errorf("a storage failure on download answered %d with %q", status, body)
	}

	_, fields, _ := s.proxy.PostObject("", "courses/1.bin", time.Minute)

	uploadStatus, answer := s.upload(t, fields, []byte("data"))
	if uploadStatus != http.StatusBadGateway || strings.Contains(answer, "secret detail") {
		t.Errorf("a storage failure on upload answered %d with %q", uploadStatus, answer)
	}

	if len(logged) != 2 {
		t.Errorf("%d storage errors were logged, want 2", len(logged))
	}
}

func TestOnlyUploadsAndDownloadsAreServed(t *testing.T) {
	s := newSetup(t)
	s.storage.files["courses/1.bin"] = []byte("one")

	good, _ := s.proxy.GetObject("", "courses/1.bin", time.Minute)

	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPost} {
		request, _ := http.NewRequest(method, good.String(), nil)

		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}

		response.Body.Close()

		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s on a download link answered %d, want 404", method, response.StatusCode)
		}
	}

	if string(s.storage.files["courses/1.bin"]) != "one" {
		t.Error("the file was changed")
	}
}

func TestWithNoLimitSetTheDefaultLimitIsUsed(t *testing.T) {
	s := newSetup(t)
	s.proxy.MaxUploadBytes = 0

	_, fields, _ := s.proxy.PostObject("", "courses/1.bin", time.Minute)

	if status, answer := s.upload(t, fields, make([]byte, 2000)); status != http.StatusNoContent {
		t.Errorf("a file of 2000 bytes answered %d, want 204: %s", status, answer)
	}

	if status, _ := s.upload(t, fields, make([]byte, DefaultMaxUploadBytes+1)); status != http.StatusRequestEntityTooLarge {
		t.Errorf("a file one byte over the default limit answered %d, want 413", status)
	}
}

// * A link signed for no file at all must not be usable for one
func TestALinkForAnEmptyKeyIsRefused(t *testing.T) {
	s := newSetup(t)
	s.storage.files[""] = []byte("nameless")

	_, fields, _ := s.proxy.PostObject("", "", time.Minute)

	if status, _ := s.upload(t, fields, []byte("data")); status != http.StatusForbidden {
		t.Errorf("an upload for an empty key answered %d, want 403", status)
	}

	if string(s.storage.files[""]) != "nameless" {
		t.Error("an upload for an empty key was stored")
	}

	download, _ := s.proxy.GetObject("", "", time.Minute)

	// * The link is made without the slash that ends an empty key, and
	// * would not reach a download without it
	download.Path = Path + "/"

	if status, body, _ := get(t, download.String()); status != http.StatusForbidden || bytes.Contains(body, []byte("nameless")) {
		t.Errorf("a download for an empty key answered %d with %q, want 403", status, body)
	}
}

func TestTransfersThatWorkLogNothing(t *testing.T) {
	s := newSetup(t)

	var logged []error
	s.proxy.LogError = func(err error) { logged = append(logged, err) }

	_, fields, _ := s.proxy.PostObject("", "courses/1.bin", time.Minute)

	if status, _ := s.upload(t, fields, []byte("data")); status != http.StatusNoContent {
		t.Fatalf("upload answered %d", status)
	}

	download, _ := s.proxy.GetObject("", "courses/1.bin", time.Minute)

	if status, body, _ := get(t, download.String()); status != http.StatusOK || string(body) != "data" {
		t.Fatalf("download answered %d with %q", status, body)
	}

	if len(logged) != 0 {
		t.Errorf("a working upload and download logged %v", logged)
	}
}

// * Sends a form whose parts are given in order, as name and value
func (s *setup) uploadParts(t *testing.T, contentType string, parts [][2]string) int {
	t.Helper()

	const boundary = "--------BOUNDARY--------45143351D5E385E3"

	var body bytes.Buffer

	for _, part := range parts {
		body.WriteString("--" + boundary + "\r\nContent-Disposition: form-data; name=\"" + part[0] + "\"\r\n\r\n" + part[1] + "\r\n")
	}

	body.WriteString("--" + boundary + "--\r\n")

	response, err := http.Post(s.server.URL+Path, contentType+"; boundary="+boundary, &body)
	if err != nil {
		t.Fatal(err)
	}

	response.Body.Close()

	return response.StatusCode
}

func TestAFormWithItsFileBeforeItsFieldsIsTaken(t *testing.T) {
	s := newSetup(t)

	_, fields, _ := s.proxy.PostObject("", "courses/1.bin", time.Minute)

	status := s.uploadParts(t, "multipart/form-data", [][2]string{
		{"file", "data"},
		{"key", fields["key"]},
		{"expires", fields["expires"]},
		{"signature", fields["signature"]},
	})
	if status != http.StatusNoContent || string(s.storage.files["courses/1.bin"]) != "data" {
		t.Errorf("answered %d and stored %q, want 204 and the file", status, s.storage.files["courses/1.bin"])
	}
}

func TestAFormMayHave16FieldsBesidesTheFileAndNoMore(t *testing.T) {
	s := newSetup(t)

	_, fields, _ := s.proxy.PostObject("", "courses/1.bin", time.Minute)

	form := func(fieldCount int) [][2]string {
		parts := [][2]string{{"key", fields["key"]}, {"expires", fields["expires"]}, {"signature", fields["signature"]}}

		for i := len(parts); i < fieldCount; i++ {
			parts = append(parts, [2]string{"extra" + strconv.Itoa(i), "x"})
		}

		return append(parts, [2]string{"file", "data"})
	}

	if status := s.uploadParts(t, "multipart/form-data", form(17)); status != http.StatusBadRequest {
		t.Errorf("17 fields answered %d, want 400", status)
	}

	if len(s.storage.files) != 0 {
		t.Error("a form with 17 fields was stored")
	}

	if status := s.uploadParts(t, "multipart/form-data", form(16)); status != http.StatusNoContent {
		t.Errorf("16 fields answered %d, want 204", status)
	}
}

// * A boundary does not make something else a form
func TestAnUploadThatIsNotAMultipartFormIsRefusedEvenWithABoundary(t *testing.T) {
	s := newSetup(t)

	_, fields, _ := s.proxy.PostObject("", "courses/1.bin", time.Minute)

	for _, contentType := range []string{"text/plain", "multipart/mixed"} {
		status := s.uploadParts(t, contentType, [][2]string{
			{"key", fields["key"]},
			{"expires", fields["expires"]},
			{"signature", fields["signature"]},
			{"file", "data"},
		})
		if status != http.StatusBadRequest {
			t.Errorf("%s answered %d, want 400", contentType, status)
		}
	}

	if len(s.storage.files) != 0 {
		t.Error("something that is not a form was stored")
	}
}
