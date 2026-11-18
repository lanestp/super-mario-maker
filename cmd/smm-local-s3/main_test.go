package main

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3afero"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/spf13/afero"
)

// * Runs a function and returns what it printed
func printedBy(t *testing.T, run func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	printed := make(chan string)
	go func() {
		data, _ := io.ReadAll(reader)
		printed <- string(data)
	}()

	previous := os.Stdout
	os.Stdout = writer

	run()

	os.Stdout = previous
	writer.Close()

	return <-printed
}

func TestTheLocalS3RefusesToListenWhereOtherMachinesCanReachIt(t *testing.T) {
	for _, address := range []string{"0.0.0.0:9000", "192.0.2.1:9000", ":9000", "s3.example.com:9000"} {
		directory := filepath.Join(t.TempDir(), "s3")

		err := run(address, directory, "smm", "courses/900000.bin")
		if err == nil || err.Error() != "refusing to listen on "+address+", as this server has no access control. Use 127.0.0.1" {
			t.Errorf("%s: got %v, want it refused", address, err)
		}

		if _, statError := os.Stat(directory); statError == nil {
			t.Errorf("%s: the directory was made for a server that was refused", address)
		}
	}
}

func TestTheLocalS3NeedsAnAddressWithAPort(t *testing.T) {
	if err := run("127.0.0.1", t.TempDir(), "smm", "courses/900000.bin"); err == nil || !strings.Contains(err.Error(), "missing port in address") {
		t.Errorf("got %v, want the port asked for", err)
	}
}

func TestTheLocalS3StopsWhenItsAddressIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer taken.Close()

	directory := filepath.Join(t.TempDir(), "s3")

	if err := run(taken.Addr().String(), directory, "smm", "courses/900000.bin"); err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Errorf("got %v, want the address in use", err)
	}

	// * The directory is made before listening
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		t.Errorf("the directory for files was not made: %v", err)
	}
}

// * The stand-in as run serves it, keeping files in a directory, and a
// * client for it
func newLocalS3(t *testing.T) (*httptest.Server, *minio.Client, string) {
	t.Helper()

	directory := t.TempDir()

	backend, err := s3afero.MultiBucket(afero.NewBasePathFs(afero.NewOsFs(), directory))
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(logRequests(nameUploadedFiles(gofakes3.New(backend).Server())))
	t.Cleanup(server.Close)

	client, err := minio.New(strings.TrimPrefix(server.URL, "http://"), &minio.Options{Creds: credentials.NewStaticV4("local", "local-secret", "")})
	if err != nil {
		t.Fatal(err)
	}

	return server, client, directory
}

func TestTheLocalS3MakesTheBucketAndAPlaceholderEventCourseMetadataFile(t *testing.T) {
	server, client, directory := newLocalS3(t)
	address := strings.TrimPrefix(server.URL, "http://")

	var err error
	printed := printedBy(t, func() { err = prepare(address, "smm", "courses/900000.bin") })

	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(printed, "Created a placeholder courses/900000.bin\n") {
		t.Errorf("did not say the placeholder was made. Printed:\n%s", printed)
	}

	// * The game server only needs it to be there, and it can not be empty
	info, err := client.StatObject(context.Background(), "smm", "courses/900000.bin", minio.StatObjectOptions{})
	if err != nil || info.Size != 1 {
		t.Errorf("the placeholder is %d bytes (%v), want 1", info.Size, err)
	}

	// * It is kept in the directory, and not only in memory
	found := false
	filepath.WalkDir(directory, func(path string, entry fs.DirEntry, _ error) error {
		found = found || (entry != nil && entry.Name() == "900000.bin")
		return nil
	})

	if !found {
		t.Error("the placeholder is not kept in the directory")
	}
}

func TestTheLocalS3LeavesARealEventCourseMetadataFileAlone(t *testing.T) {
	server, client, _ := newLocalS3(t)
	address := strings.TrimPrefix(server.URL, "http://")

	printedBy(t, func() {
		if err := prepare(address, "smm", "courses/900000.bin"); err != nil {
			t.Fatal(err)
		}
	})

	real := []byte("the real event course metadata")
	if _, err := client.PutObject(context.Background(), "smm", "courses/900000.bin", bytes.NewReader(real), int64(len(real)), minio.PutObjectOptions{}); err != nil {
		t.Fatal(err)
	}

	var err error
	printed := printedBy(t, func() { err = prepare(address, "smm", "courses/900000.bin") })

	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(printed, "Created a placeholder") {
		t.Error("made a placeholder over the real file")
	}

	info, _ := client.StatObject(context.Background(), "smm", "courses/900000.bin", minio.StatObjectOptions{})
	if info.Size != int64(len(real)) {
		t.Errorf("the file is %d bytes, want the %d of the real one", info.Size, len(real))
	}
}

func TestTheLocalS3CanNotBePreparedWhenItIsNotThere(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	address := strings.TrimPrefix(closed.URL, "http://")
	closed.Close()

	if err := prepare(address, "smm", "courses/900000.bin"); err == nil {
		t.Error("prepared a server that is not there")
	}
}

// * An upload form as a console sends it: the file part has no file name
func consoleUploadForm(t *testing.T, key string, file []byte) (*bytes.Buffer, string) {
	t.Helper()

	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)

	form.WriteField("key", key)

	part, err := form.CreatePart(map[string][]string{"Content-Disposition": {`form-data; name="file"`}})
	if err != nil {
		t.Fatal(err)
	}

	part.Write(file)
	form.Close()

	return body, form.FormDataContentType()
}

func TestTheLocalS3TakesAnUploadFromAConsoleThatGivesNoFileName(t *testing.T) {
	server, client, _ := newLocalS3(t)

	if err := client.MakeBucket(context.Background(), "smm", minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}

	file := []byte("a course file")
	body, contentType := consoleUploadForm(t, "courses/940000.bin", file)

	var response *http.Response
	printedBy(t, func() {
		var err error
		response, err = http.Post(server.URL+"/smm", contentType, body)
		if err != nil {
			t.Fatal(err)
		}
	})

	response.Body.Close()

	if response.StatusCode >= 300 {
		t.Fatalf("the upload was refused with %d", response.StatusCode)
	}

	object, err := client.GetObject(context.Background(), "smm", "courses/940000.bin", minio.GetObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}

	stored, err := io.ReadAll(object)
	if err != nil || !bytes.Equal(stored, file) {
		t.Errorf("stored %q (%v), want the uploaded file", stored, err)
	}
}

func TestTheStandInRefusesAnUploadWithoutAFileNameOnItsOwn(t *testing.T) {
	directory := t.TempDir()

	backend, err := s3afero.MultiBucket(afero.NewBasePathFs(afero.NewOsFs(), directory))
	if err != nil {
		t.Fatal(err)
	}

	if err := backend.CreateBucket("smm"); err != nil {
		t.Fatal(err)
	}

	body, contentType := consoleUploadForm(t, "courses/940000.bin", []byte("a course file"))

	request := httptest.NewRequest(http.MethodPost, "/smm", body)
	request.Header.Set("Content-Type", contentType)

	response := httptest.NewRecorder()
	gofakes3.New(backend).Server().ServeHTTP(response, request)

	// * Which is why the file part is given a name
	if response.Code < 400 {
		t.Errorf("the stand-in took the upload with %d, so naming the file part is no longer needed", response.Code)
	}
}

func TestAnUploadFormThatCanNotBeReadIsRefused(t *testing.T) {
	reached := false
	handler := nameUploadedFiles(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	// * The file part is cut off before the form ends
	request := httptest.NewRequest(http.MethodPost, "/smm", strings.NewReader("--boundary\r\nContent-Disposition: form-data; name=\"file\"\r\n\r\npart of a fi"))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.HasPrefix(response.Body.String(), "could not read the upload form: ") || reached {
		t.Errorf("answered %d %q, want 400 before the stand-in is reached", response.Code, response.Body.String())
	}
}

func TestAFormWithAFileNameIsPassedOnAsItWas(t *testing.T) {
	var got []byte
	var gotName string

	handler := nameUploadedFiles(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}

		gotName = header.Filename
		got, _ = io.ReadAll(file)
	}))

	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	form.WriteField("key", "courses/1.bin")
	part, _ := form.CreateFormFile("file", "course.bin")
	part.Write([]byte("contents"))
	form.Close()

	request := httptest.NewRequest(http.MethodPost, "/smm", body)
	request.Header.Set("Content-Type", form.FormDataContentType())

	handler.ServeHTTP(httptest.NewRecorder(), request)

	if gotName != "course.bin" || string(got) != "contents" {
		t.Errorf("the file came through as %q holding %q", gotName, got)
	}
}

func TestOnlyAFormPostedAsMultipartIsRewritten(t *testing.T) {
	var got []byte

	handler := nameUploadedFiles(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
	}))

	// * Neither is a form a console uploads, so neither is read as one
	for name, sent := range map[string]struct{ method, contentType string }{
		"a form put":  {http.MethodPut, "multipart/form-data; boundary=x"},
		"a file post": {http.MethodPost, "application/octet-stream"},
	} {
		request := httptest.NewRequest(sent.method, "/smm/courses/1.bin", strings.NewReader("not a form"))
		request.Header.Set("Content-Type", sent.contentType)

		got = nil
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusOK || string(got) != "not a form" {
			t.Errorf("%s: answered %d and passed on %q, want it passed on as it was", name, response.Code, got)
		}
	}
}

func TestEachRequestIsPrintedWithoutItsQueryString(t *testing.T) {
	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte("fine"))
	}))

	request := httptest.NewRequest(http.MethodGet, "/smm/courses/940000.bin?X-Amz-Signature=secret", nil)

	printed := printedBy(t, func() { handler.ServeHTTP(httptest.NewRecorder(), request) })

	if !strings.Contains(printed, " GET /smm/courses/940000.bin -> 200 (0 bytes sent by client)\n") || !strings.HasPrefix(printed, "[S3] ") {
		t.Errorf("printed %q", printed)
	}

	if strings.Contains(printed, "secret") || strings.Contains(printed, "refused") {
		t.Errorf("printed the signature or details of a request that was not refused: %q", printed)
	}
}

func TestARefusedRequestIsPrintedWithWhatWasSentAndTheAnswer(t *testing.T) {
	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// * Read a byte at a time, so that what is kept of it is kept in many parts
		io.Copy(io.Discard, iotest.OneByteReader(r.Body))
		http.Error(w, "policy refused", http.StatusForbidden)
	}))

	sent := strings.Repeat("x", maxLoggedBytes+500)

	request := httptest.NewRequest(http.MethodPost, "/smm", strings.NewReader(sent))
	request.Header.Set("X-Console", "Wii U")

	printed := printedBy(t, func() { handler.ServeHTTP(httptest.NewRecorder(), request) })

	if !strings.Contains(printed, "POST /smm -> 403 (4596 bytes sent by client)") {
		t.Errorf("did not print the refused request. Printed:\n%s", printed)
	}

	if !strings.Contains(printed, `[S3] refused request headers: map["X-Console":["Wii U"]]`) || !strings.Contains(printed, `[S3] answer: "policy refused\n"`) {
		t.Errorf("did not print the headers and the answer. Printed:\n%s", printed)
	}

	// * Only the start of what was sent is printed
	if !strings.Contains(printed, `[S3] refused request start: "`+strings.Repeat("x", maxLoggedBytes)+`"`) {
		t.Errorf("did not print the first %d bytes sent", maxLoggedBytes)
	}
}

func TestTheAnswerToABadRequestIsPrintedOnlyUpToItsLimit(t *testing.T) {
	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(strings.Repeat("a", maxLoggedBytes)))
		w.Write([]byte("past the limit"))
	}))

	printed := printedBy(t, func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/smm/courses/900000.bin", nil))
	})

	if !strings.Contains(printed, `[S3] answer: "`+strings.Repeat("a", maxLoggedBytes)+`"`) {
		t.Errorf("did not print the first %d bytes of the answer, and only those. Printed:\n%s", maxLoggedBytes, printed)
	}
}

func TestARefusedHeadRequestIsPrintedWithoutDetails(t *testing.T) {
	handler := logRequests(http.NotFoundHandler())

	printed := printedBy(t, func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodHead, "/smm/courses/900000.bin", nil))
	})

	// * A game server checking for a file that is not there yet is not worth the detail
	if !strings.Contains(printed, "HEAD /smm/courses/900000.bin -> 404") || strings.Contains(printed, "refused request") {
		t.Errorf("printed %q", printed)
	}
}
