package importsvc_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

	if globals.Logger == nil {
		globals.Logger = plogger.NewLogger()
	}

	opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { opened.Close() })

	s := &setup{store: opened, files: map[string][]byte{}}

	handler, err := importsvc.Handler(importsvc.Config{
		Token: "the-token",
		Seeder: &seed.Seeder{
			Store:   opened,
			KeyBase: "courses",
			PutFile: func(key string, data []byte) error {
				s.files[key] = data
				return nil
			},
		},
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
