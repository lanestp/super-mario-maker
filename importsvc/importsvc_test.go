package importsvc_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/coursefile"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/importsvc"
	"github.com/PretendoNetwork/super-mario-maker/seed"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

type setup struct {
	store  store.Store
	files  map[string][]byte
	server *httptest.Server
}

func newSetup(t *testing.T) *setup {
	t.Helper()

	return newSetupWith(t, nil, nil)
}

// * putFile, when given, stores course files in place of keeping them in
// * the setup's files, and logError is the service's LogError
func newSetupWith(t *testing.T, putFile func(key string, data []byte) error, logError func(error)) *setup {
	t.Helper()

	if globals.Logger == nil {
		globals.Logger = plogger.NewLogger()
	}

	opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { opened.Close() })

	s := &setup{store: opened, files: map[string][]byte{}}

	if putFile == nil {
		putFile = func(key string, data []byte) error {
			s.files[key] = data
			return nil
		}
	}

	handler, err := importsvc.Handler(importsvc.Config{
		Token: "the-token",
		Seeder: &seed.Seeder{
			Store:   opened,
			KeyBase: "courses",
			PutFile: putFile,
		},
		LogError: logError,
	})
	if err != nil {
		t.Fatal(err)
	}

	s.server = httptest.NewServer(handler)
	t.Cleanup(s.server.Close)

	return s
}

func courseFile(t *testing.T) []byte {
	t.Helper()

	course := &coursefile.Course{
		Preview:   make([]byte, coursefile.PictureSize),
		MainArea:  make([]byte, coursefile.AreaSize),
		SubArea:   make([]byte, coursefile.AreaSize),
		Thumbnail: make([]byte, coursefile.PictureSize),
	}

	file, err := course.Pack()
	if err != nil {
		t.Fatal(err)
	}

	return file
}

func (s *setup) send(t *testing.T, token string, course importsvc.Course, file []byte) (int, string) {
	t.Helper()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)

	details, _ := json.Marshal(course)
	form.WriteField("course", string(details))

	if file != nil {
		part, _ := form.CreateFormFile("file", "course.bin")
		part.Write(file)
	}

	form.Close()

	request, _ := http.NewRequest(http.MethodPost, s.server.URL+importsvc.Path, &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+token)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer response.Body.Close()

	answer, _ := io.ReadAll(response.Body)

	return response.StatusCode, string(answer)
}

func (s *setup) exists(t *testing.T, token string, id string) int {
	t.Helper()

	request, _ := http.NewRequest(http.MethodGet, s.server.URL+importsvc.Path+"/"+id, nil)
	request.Header.Set("Authorization", "Bearer "+token)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	response.Body.Close()

	return response.StatusCode
}

func validCourse() importsvc.Course {
	return importsvc.Course{
		DataID:   37585837,
		Name:     "Leap Of Faith",
		Uploaded: time.Date(2016, 1, 2, 3, 4, 5, 0, time.UTC),
		Maker:    importsvc.Player{PID: 1767174539, Name: "Sharkboy55555"},
		Stars:    12,
		Clears:   51,
		Attempts: 464,
		Record:   &importsvc.Record{Milliseconds: 42911, Holder: importsvc.Player{Name: "speedy"}},
	}
}

func TestACourseSentToTheServiceIsChecked(t *testing.T) {
	s := newSetup(t)
	file := courseFile(t)

	if status := s.exists(t, "the-token", "37585837"); status != http.StatusNotFound {
		t.Errorf("before importing, asking for the course answered %d, want 404", status)
	}

	if status, answer := s.send(t, "the-token", validCourse(), file); status != http.StatusCreated {
		t.Fatalf("answered %d: %s", status, answer)
	}

	if status := s.exists(t, "the-token", "37585837"); status != http.StatusOK {
		t.Errorf("after importing, asking for the course answered %d, want 200", status)
	}

	if status, _ := s.send(t, "the-token", validCourse(), file); status != http.StatusConflict {
		t.Errorf("sending it again answered %d, want 409", status)
	}

	bad := validCourse()
	bad.DataID = 37585838
	bad.Clears = 9999

	if status, answer := s.send(t, "the-token", bad, file); status != http.StatusUnprocessableEntity {
		t.Errorf("a course whose numbers do not add up answered %d: %s", status, answer)
	}

	other := validCourse()
	other.DataID = 37585839

	if status, _ := s.send(t, "the-token", other, []byte("not a course")); status != http.StatusUnprocessableEntity {
		t.Errorf("a file that is not a course answered %d, want 422", status)
	}

	if status, _ := s.send(t, "the-token", other, nil); status != http.StatusBadRequest {
		t.Errorf("a form with no file answered %d, want 400", status)
	}

	if len(s.files) != 1 {
		t.Errorf("%d files were stored, want only the one valid course's", len(s.files))
	}
}

func TestTheServiceNeedsTheToken(t *testing.T) {
	s := newSetup(t)

	for _, token := range []string{"", "the-toke", "the-token-and-more"} {
		if status, _ := s.send(t, token, validCourse(), courseFile(t)); status != http.StatusUnauthorized {
			t.Errorf("token %q answered %d, want 401", token, status)
		}

		if status := s.exists(t, token, "37585837"); status != http.StatusUnauthorized {
			t.Errorf("asking with token %q answered %d, want 401", token, status)
		}
	}

	if len(s.files) != 0 {
		t.Error("a file was stored without the token")
	}
}

func (s *setup) recordAttempt(t *testing.T, attempt importsvc.Attempt) int {
	t.Helper()

	body, _ := json.Marshal(attempt)
	request, _ := http.NewRequest(http.MethodPost, s.server.URL+importsvc.AttemptsPath, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer the-token")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	response.Body.Close()

	return response.StatusCode
}

func (s *setup) attempts(t *testing.T, query string) []importsvc.Attempt {
	t.Helper()

	request, _ := http.NewRequest(http.MethodGet, s.server.URL+importsvc.AttemptsPath+"?"+query, nil)
	request.Header.Set("Authorization", "Bearer the-token")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer response.Body.Close()

	var attempts []importsvc.Attempt
	if err := json.NewDecoder(response.Body).Decode(&attempts); err != nil {
		t.Fatal(err)
	}

	return attempts
}

func TestCoursesThatWereNotBroughtInCanBeTriedAgain(t *testing.T) {
	s := newSetup(t)

	entry := json.RawMessage(`{"levelid":37585837,"name":"Leap Of Faith"}`)

	for _, attempt := range []importsvc.Attempt{
		{DataID: 37585837, Outcome: store.ImportFailed, Detail: "the archive did not answer", Entry: entry},
		{DataID: 11111111, Outcome: store.ImportNotArchived, Entry: json.RawMessage(`{}`)},
		{DataID: 22222222, Outcome: store.ImportRefused, Detail: "numbers do not add up", Entry: json.RawMessage(`{}`)},
	} {
		if status := s.recordAttempt(t, attempt); status != http.StatusNoContent {
			t.Fatalf("recording %d answered %d", attempt.DataID, status)
		}
	}

	if status := s.recordAttempt(t, importsvc.Attempt{DataID: 1, Outcome: "lost"}); status != http.StatusBadRequest {
		t.Errorf("an unknown outcome answered %d, want 400", status)
	}

	toRetry := s.attempts(t, "outcome=failed&outcome=not_archived")
	if len(toRetry) != 2 {
		t.Fatalf("got %d to retry, want the failed and not archived ones", len(toRetry))
	}

	for _, attempt := range toRetry {
		if attempt.DataID == 37585837 && (string(attempt.Entry) != string(entry) || attempt.Tries != 1) {
			t.Errorf("the failed course came back as %+v", attempt)
		}
	}

	// * Once a course is brought in, it is no longer listed
	if status, answer := s.send(t, "the-token", validCourse(), courseFile(t)); status != http.StatusCreated {
		t.Fatalf("importing answered %d: %s", status, answer)
	}

	if left := s.attempts(t, "outcome=failed&outcome=not_archived&outcome=refused"); len(left) != 2 {
		t.Errorf("after bringing the course in, %d attempts are left, want 2", len(left))
	}
}

func TestARunCarriesOnFromItsCursor(t *testing.T) {
	s := newSetup(t)

	call := func(method, body string) (int, string) {
		t.Helper()

		request, _ := http.NewRequest(method, s.server.URL+importsvc.CursorsPath+"/positions-1000", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer the-token")

		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}

		defer response.Body.Close()

		answer, _ := io.ReadAll(response.Body)

		return response.StatusCode, strings.TrimSpace(string(answer))
	}

	if status, answer := call(http.MethodGet, ""); status != http.StatusOK || answer != `{"position":0}` {
		t.Errorf("a run that has not started answered %d %s", status, answer)
	}

	if status, _ := call(http.MethodPut, `{"position":7516625}`); status != http.StatusNoContent {
		t.Fatalf("saving the cursor answered %d", status)
	}

	if status, answer := call(http.MethodGet, ""); status != http.StatusOK || answer != `{"position":7516625}` {
		t.Errorf("after saving, the cursor answered %d %s", status, answer)
	}

	if status, _ := call(http.MethodPut, `{"position":-5}`); status != http.StatusBadRequest {
		t.Errorf("a negative position answered %d, want 400", status)
	}

	// * A run can be sent back to the start
	if status, _ := call(http.MethodPut, `{"position":0}`); status != http.StatusNoContent {
		t.Errorf("a position of 0 answered %d, want 204", status)
	}

	if status, answer := call(http.MethodGet, ""); status != http.StatusOK || answer != `{"position":0}` {
		t.Errorf("after going back to the start, the cursor answered %d %s", status, answer)
	}
}

func TestTheServiceIsNotMadeWithoutASeederAndAToken(t *testing.T) {
	if _, err := importsvc.Handler(importsvc.Config{Token: "the-token"}); err == nil {
		t.Error("a service with no seeder was made")
	}

	if _, err := importsvc.Handler(importsvc.Config{Seeder: &seed.Seeder{}}); err == nil {
		t.Error("a service with no token was made")
	}

	if _, err := importsvc.Handler(importsvc.Config{Seeder: &seed.Seeder{}, Token: "the-token"}); err != nil {
		t.Errorf("a service with a seeder and a token was refused: %v", err)
	}
}

func TestStorageFailuresAreLoggedAndNothingElseIs(t *testing.T) {
	failing := false
	var logged []error

	s := newSetupWith(t, func(string, []byte) error {
		if failing {
			return errors.New("storage is down")
		}

		return nil
	}, func(err error) { logged = append(logged, err) })

	if status, answer := s.send(t, "the-token", validCourse(), courseFile(t)); status != http.StatusCreated {
		t.Fatalf("importing answered %d: %s", status, answer)
	}

	if len(logged) != 0 {
		t.Errorf("a course that went in logged %v", logged)
	}

	failing = true

	other := validCourse()
	other.DataID = 37585839

	if status, _ := s.send(t, "the-token", other, courseFile(t)); status != http.StatusInternalServerError {
		t.Errorf("a storage failure answered %d, want 500", status)
	}

	if len(logged) != 1 {
		t.Errorf("a storage failure logged %d errors, want 1", len(logged))
	}
}

func TestAStorageFailureIsAnsweredWhenNothingIsToldAboutErrors(t *testing.T) {
	s := newSetupWith(t, func(string, []byte) error { return errors.New("storage is down") }, nil)

	if status, _ := s.send(t, "the-token", validCourse(), courseFile(t)); status != http.StatusInternalServerError {
		t.Errorf("a storage failure answered %d, want 500", status)
	}
}

func TestACourseWithNoRecordIsTaken(t *testing.T) {
	s := newSetup(t)

	course := validCourse()
	course.Record = nil

	if status, answer := s.send(t, "the-token", course, courseFile(t)); status != http.StatusCreated {
		t.Errorf("a course with no record answered %d: %s", status, answer)
	}
}

// * A form with a course file of the largest size is read, and
// * so is checked. One with a much larger file is not read at all
func TestACourseFileOfOneMebibyteIsReadAndALargerOneIsNot(t *testing.T) {
	s := newSetup(t)

	if status, answer := s.send(t, "the-token", validCourse(), make([]byte, 1<<20)); status != http.StatusUnprocessableEntity {
		t.Errorf("a file of 1 MiB answered %d, want 422 for being checked and found not to be a course: %s", status, answer)
	}

	if status, _ := s.send(t, "the-token", validCourse(), make([]byte, 1<<20+64<<10)); status != http.StatusBadRequest {
		t.Errorf("a file 64 KiB over 1 MiB answered %d, want 400", status)
	}
}

func TestAttemptsToRetryKeepToTheMostTriesAndTheLimitAskedFor(t *testing.T) {
	s := newSetup(t)

	// * Course n has been tried n times
	for dataID := range uint64(4) {
		for range dataID + 1 {
			if err := s.store.RecordImportAttempt(store.ImportAttempt{DataID: dataID + 1, Outcome: store.ImportFailed, Entry: []byte(`{}`)}); err != nil {
				t.Fatal(err)
			}
		}
	}

	for query, want := range map[string]int{
		"":              2,
		"&max_tries=0":  2,
		"&max_tries=-1": 2,
		"&max_tries=1":  0,
		"&max_tries=2":  1,
		"&max_tries=5":  4,
	} {
		if got := s.attempts(t, "outcome=failed"+query); len(got) != want {
			t.Errorf("%q listed %d attempts, want %d", query, len(got), want)
		}
	}

	// * Enough courses, tried once, to fill more than the default limit of 100
	for dataID := range uint64(101) {
		if err := s.store.RecordImportAttempt(store.ImportAttempt{DataID: 1000 + dataID, Outcome: store.ImportFailed, Entry: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}

	for query, want := range map[string]int{
		"":             100,
		"&limit=0":     100,
		"&limit=-1":    100,
		"&limit=many":  100,
		"&limit=1":     1,
		"&limit=2":     2,
		"&limit=10000": 105,
		"&limit=10001": 100,
	} {
		if got := s.attempts(t, "outcome=failed&max_tries=5"+query); len(got) != want {
			t.Errorf("%q listed %d attempts, want %d", query, len(got), want)
		}
	}
}
