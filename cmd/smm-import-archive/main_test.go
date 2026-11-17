package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/coursefile"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/importsvc"
	"github.com/PretendoNetwork/super-mario-maker/seed"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

// * An index of courses at positions 100 to 109, one per position, with
// * IDs 5000 to 5009, and a Wayback Machine that has some of their files
type archive struct {
	mutex    sync.Mutex
	archived map[uint64]bool
	asked    []int64
	file     []byte
}

func (a *archive) index(w http.ResponseWriter, r *http.Request) {
	position, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/index/"), 10, 64)

	a.mutex.Lock()
	a.asked = append(a.asked, position)
	a.mutex.Unlock()

	if position < 100 || position > 109 {
		w.Write([]byte(`{"error":"No Random Level Found!"}`))
		return
	}

	id := 5000 + position - 100
	json.NewEncoder(w).Encode([]map[string]any{{
		"url":            fmt.Sprintf("https://cdn.example.com/data/%d", id),
		"name":           fmt.Sprintf("Course %d", id),
		"creator":        "maker" + strconv.FormatInt(id, 10),
		"levelid":        id,
		"creatorid":      1700000000 + id,
		"clears":         3,
		"failures":       7,
		"total_attempts": 10,
		"uploadTime":     "2016-01-02T03:04:05.000Z",
		"stars":          2,
	}})
}

func (a *archive) wayback(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseUint(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], 10, 64)

	a.mutex.Lock()
	defer a.mutex.Unlock()

	if !a.archived[id] {
		http.NotFound(w, r)
		return
	}

	w.Write(a.file)
}

type setup struct {
	archive  *archive
	store    store.Store
	importer *importer
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

	a := &archive{archived: map[uint64]bool{}, file: file}
	for id := uint64(5000); id <= 5009; id++ {
		a.archived[id] = id != 5003 && id != 5007
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/index/", a.index)
	mux.HandleFunc("/wayback/", a.wayback)

	service, err := importsvc.Handler(importsvc.Config{
		Token: "the-token",
		Seeder: &seed.Seeder{
			Store:   opened,
			KeyBase: "courses",
			PutFile: func(string, []byte) error { return nil },
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	mux.Handle("/v1/import/", service)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return &setup{
		archive: a,
		store:   opened,
		importer: &importer{
			client:  server.Client(),
			index:   server.URL + "/index/",
			wayback: server.URL + "/wayback/",
			service: server.URL,
			token:   "the-token",
		},
	}
}

func (s *setup) onServer(id uint64) bool {
	_, nexError := s.store.GetObjectInfoByDataID(types.UInt64(id))
	return nexError == nil
}

func TestAWalkBringsInEveryArchivedCourseInOrder(t *testing.T) {
	s := newSetup(t)

	s.importer.walk(context.Background(), "test", 100, 109, 1, 0)

	if fmt.Sprint(s.archive.asked) != "[100 101 102 103 104 105 106 107 108 109]" {
		t.Errorf("asked the index for %v, want every position in order", s.archive.asked)
	}

	for id := uint64(5000); id <= 5009; id++ {
		archived := id != 5003 && id != 5007
		if s.onServer(id) != archived {
			t.Errorf("course %d on the server: %v, want %v", id, s.onServer(id), archived)
		}
	}

	if s.importer.imported != 8 || s.importer.notArchived != 2 {
		t.Errorf("brought in %d and found %d not archived, want 8 and 2", s.importer.imported, s.importer.notArchived)
	}

	// * The ones not archived are kept, with their index entry, for retrying
	attempts, err := s.store.ImportAttemptsToRetry([]string{store.ImportNotArchived}, 5, 10)
	if err != nil || len(attempts) != 2 {
		t.Fatalf("kept %d attempts (%v), want the 2 not archived", len(attempts), err)
	}

	if position, _ := s.store.ImportCursor("test"); position != 110 {
		t.Errorf("the run is at position %d, want 110, past the last one", position)
	}
}

func TestAStoppedWalkCarriesOnWhereItGotTo(t *testing.T) {
	s := newSetup(t)

	// * Stops after 3 courses, at position 102
	s.importer.walk(context.Background(), "test", 100, 109, 1, 3)

	if position, _ := s.store.ImportCursor("test"); position != 103 {
		t.Fatalf("the run stopped at position %d, want 103", position)
	}

	s.archive.asked = nil
	s.importer.imported = 0

	s.importer.walk(context.Background(), "test", 100, 109, 1, 0)

	if len(s.archive.asked) == 0 || s.archive.asked[0] != 103 {
		t.Errorf("the second run started at %v, want position 103", s.archive.asked)
	}

	if s.importer.imported != 5 {
		t.Errorf("the second run brought in %d, want the 5 archived courses left", s.importer.imported)
	}
}

func TestAStepTakesAnEvenSample(t *testing.T) {
	s := newSetup(t)

	s.importer.walk(context.Background(), "sample", 100, 109, 3, 0)

	if fmt.Sprint(s.archive.asked) != "[100 103 106 109]" {
		t.Errorf("asked the index for %v, want every third position", s.archive.asked)
	}

	if !s.onServer(5000) || !s.onServer(5006) || s.onServer(5001) {
		t.Error("the sample brought in the wrong courses")
	}
}

func TestRetryBringsInCoursesThatHaveSinceTurnedUp(t *testing.T) {
	s := newSetup(t)

	s.importer.walk(context.Background(), "test", 100, 109, 1, 0)

	s.archive.mutex.Lock()
	s.archive.archived[5003] = true
	s.archive.mutex.Unlock()

	s.importer.imported = 0
	s.importer.retry(context.Background(), 3, 0)

	if !s.onServer(5003) || s.onServer(5007) {
		t.Errorf("after retrying, 5003 on the server: %v, 5007: %v; want only 5003", s.onServer(5003), s.onServer(5007))
	}

	attempts, _ := s.store.ImportAttemptsToRetry([]string{store.ImportNotArchived}, 5, 10)
	if len(attempts) != 1 || attempts[0].DataID != 5007 || attempts[0].Tries != 2 {
		t.Errorf("left to retry: %+v, want only 5007, tried twice", attempts)
	}
}
