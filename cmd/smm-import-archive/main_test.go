package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// * Keeps what is logged during the test
func logged(t *testing.T) *bytes.Buffer {
	t.Helper()

	output := &bytes.Buffer{}
	log.SetOutput(output)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	return output
}

// * Stands between the importer and the import service, keeping each
// * request, and answering 500 to those it is told to refuse
type serviceRecorder struct {
	mutex    sync.Mutex
	requests []string
	cursors  []int64
}

func (s *setup) recordService(t *testing.T, refuse func(*http.Request) bool) *serviceRecorder {
	t.Helper()

	target, err := url.Parse(s.importer.service)
	if err != nil {
		t.Fatal(err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	recorder := &serviceRecorder{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.mutex.Lock()
		recorder.requests = append(recorder.requests, r.Method+" "+r.URL.RequestURI())

		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, importsvc.CursorsPath) {
			var body bytes.Buffer
			body.ReadFrom(r.Body)

			var cursor importsvc.Cursor
			json.Unmarshal(body.Bytes(), &cursor)
			recorder.cursors = append(recorder.cursors, cursor.Position)

			r.Body = io.NopCloser(&body)
		}

		recorder.mutex.Unlock()

		if refuse != nil && refuse(r) {
			http.Error(w, "not now", http.StatusInternalServerError)
			return
		}

		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	s.importer.service = server.URL

	return recorder
}

func TestTheRunsPositionIsSavedAfterEachPosition(t *testing.T) {
	s := newSetup(t)
	recorder := s.recordService(t, nil)

	s.importer.walk(context.Background(), "test", 100, 104, 2, 0)

	// * Each saved position is the next one to ask for
	if fmt.Sprint(recorder.cursors) != "[102 104 106]" {
		t.Errorf("saved positions %v, want 102, 104 and 106", recorder.cursors)
	}

	if position, _ := s.store.ImportCursor("test"); position != 106 {
		t.Errorf("the run is at position %d, want 106", position)
	}
}

func TestARunThatCanNotSaveItsPositionCarriesOn(t *testing.T) {
	s := newSetup(t)
	output := logged(t)

	s.recordService(t, func(r *http.Request) bool {
		return r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, importsvc.CursorsPath)
	})

	s.importer.walk(context.Background(), "test", 100, 109, 1, 0)

	if s.importer.imported != 8 {
		t.Errorf("brought in %d, want all 8 archived courses", s.importer.imported)
	}

	if !strings.Contains(output.String(), "the run's position could not be saved: the import service answered 500: not now") {
		t.Errorf("did not say the position could not be saved. Logged:\n%s", output)
	}

	if position, _ := s.store.ImportCursor("test"); position != 0 {
		t.Errorf("the run is at position %d, want none saved", position)
	}
}

func TestPositionsWithoutACourseAreWalkedPast(t *testing.T) {
	s := newSetup(t)

	s.importer.walk(context.Background(), "past the end", 110, 112, 1, 0)

	if fmt.Sprint(s.archive.asked) != "[110 111 112]" || s.importer.imported != 0 {
		t.Errorf("asked for %v and brought in %d, want the three positions asked for and nothing brought in", s.archive.asked, s.importer.imported)
	}

	if position, _ := s.store.ImportCursor("past the end"); position != 113 {
		t.Errorf("the run is at position %d, want 113", position)
	}
}

func TestARunStoppedPartWayThroughAPositionDoesNotSaveIt(t *testing.T) {
	s := newSetup(t)
	logged(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// * Stopped, as with Ctrl-C, while the index is asked for position
	// * 100. The answer is only sent once the handler returns, so it
	// * always arrives after the run was stopped
	index := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.archive.index(w, r)
		cancel()
	}))
	defer index.Close()

	s.importer.index = index.URL + "/index/"
	s.importer.walk(ctx, "test", 100, 109, 1, 0)

	s.archive.mutex.Lock()
	asked := fmt.Sprint(s.archive.asked)
	s.archive.mutex.Unlock()

	if asked != "[100]" || s.importer.imported != 0 || s.onServer(5000) {
		t.Errorf("a stopped run asked for %s and brought in %d", asked, s.importer.imported)
	}

	if position, _ := s.store.ImportCursor("test"); position != 0 {
		t.Errorf("a stopped run saved position %d, want 100 asked for again next time", position)
	}

	// * Being stopped is not the course's fault, so it is not kept to retry
	if attempts, _ := s.store.ImportAttemptsToRetry([]string{store.ImportFailed, store.ImportNotArchived, store.ImportRefused}, 100, 100); len(attempts) != 0 {
		t.Errorf("kept %+v for a stopped run", attempts)
	}
}

func TestAWalkStopsAfterTwentyFailuresInARow(t *testing.T) {
	s := newSetup(t)
	output := logged(t)

	var asked []string

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer broken.Close()

	s.importer.index = broken.URL + "/index/"
	s.importer.walk(context.Background(), "test", 100, 109, 1, 0)

	// * The same position is asked for each time, as it is tried again
	if len(asked) != 20 || asked[0] != "/index/100" || asked[19] != "/index/100" {
		t.Errorf("asked the index for %v, want position 100 twenty times", asked)
	}

	if !strings.Contains(output.String(), "stopping after 20 failures in a row. Running it again carries on from here") {
		t.Errorf("did not say why it stopped. Logged:\n%s", output)
	}

	if position, _ := s.store.ImportCursor("test"); position != 0 {
		t.Errorf("the run moved on to position %d without reading the index", position)
	}
}

func TestTheWaitDoublesEachTimeTheArchiveSaysToSlowDownUpToHalfAnHour(t *testing.T) {
	output := logged(t)

	i := &importer{}

	for _, want := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 30 * time.Minute, 30 * time.Minute} {
		i.slowDown("the archive answered 429")

		if i.backoff != want {
			t.Fatalf("waits %s, want %s", i.backoff, want)
		}
	}

	if !strings.Contains(output.String(), "the archive answered 429, waiting 30m0s") {
		t.Errorf("did not say how long it waits. Logged:\n%s", output)
	}
}

// * An archive that answers every request with the same status, and
// * stops the fetch soon after the first request, as the next would be
// * after the 30 second wait
func answering(t *testing.T, status int, cancel context.CancelFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	requests := &atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			time.AfterFunc(100*time.Millisecond, cancel)
		}

		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)

	return server, requests
}

func TestTheArchiveIsWaitedOutWhenItSaysToSlowDown(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, 520, 524} {
		output := logged(t)

		ctx, cancel := context.WithCancel(context.Background())
		server, requests := answering(t, status, cancel)

		i := &importer{client: server.Client()}

		body, err := i.fetch(ctx, server.URL+"/index/100")
		if !errors.Is(err, context.Canceled) || body != nil {
			t.Errorf("%d: got %q and %v, want it to be waiting when stopped", status, body, err)
		}

		if requests.Load() != 1 || i.backoff != 30*time.Second {
			t.Errorf("%d: asked %d times and waits %s, want one request and a 30 second wait", status, requests.Load(), i.backoff)
		}

		want := fmt.Sprintf("%s answered %d, waiting 30s", strings.TrimPrefix(server.URL, "http://"), status)
		if !strings.Contains(output.String(), want) {
			t.Errorf("%d: did not log %q. Logged:\n%s", status, want, output)
		}
	}
}

// * Fails to connect, and stops the fetch soon after the first try
type unreachable struct {
	tries  atomic.Int32
	cancel context.CancelFunc
}

func (u *unreachable) RoundTrip(*http.Request) (*http.Response, error) {
	if u.tries.Add(1) == 1 {
		time.AfterFunc(100*time.Millisecond, u.cancel)
	}

	return nil, errors.New("connection refused")
}

func TestAnArchiveThatCanNotBeReachedIsWaitedOut(t *testing.T) {
	output := logged(t)

	ctx, cancel := context.WithCancel(context.Background())
	transport := &unreachable{cancel: cancel}

	i := &importer{client: &http.Client{Transport: transport}}

	if _, err := i.fetch(ctx, "http://archive.example.com/index/100"); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want it to be waiting when stopped", err)
	}

	if transport.tries.Load() != 1 || i.backoff != 30*time.Second {
		t.Errorf("tried %d times and waits %s, want one try and a 30 second wait", transport.tries.Load(), i.backoff)
	}

	if !strings.Contains(output.String(), "archive.example.com could not be reached, waiting 30s") {
		t.Errorf("did not say the archive could not be reached. Logged:\n%s", output)
	}
}

func TestTheWaitIsEasedBackOnceTheArchiveAnswers(t *testing.T) {
	var userAgent string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		w.Write([]byte("the course"))
	}))
	defer server.Close()

	i := &importer{client: server.Client(), backoff: 4 * time.Millisecond}

	body, err := i.fetch(context.Background(), server.URL+"/index/100")
	if err != nil || string(body) != "the course" {
		t.Fatalf("got %q (%v)", body, err)
	}

	if i.backoff != 2*time.Millisecond {
		t.Errorf("waits %s, want half of the 4ms it waited", i.backoff)
	}

	// * The archive is told who is asking
	if userAgent != agent {
		t.Errorf("sent User-Agent %q, want %q", userAgent, agent)
	}
}

func TestAnAnswerThatIsNotASlowDownIsNotTriedAgain(t *testing.T) {
	for status, wantError := range map[int]bool{http.StatusNotFound: false, http.StatusInternalServerError: true, http.StatusBadGateway: true, http.StatusGatewayTimeout: true, http.StatusForbidden: true} {
		requests := 0

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.WriteHeader(status)
		}))

		i := &importer{client: server.Client()}

		body, err := i.fetch(context.Background(), server.URL+"/wayback/5000")

		server.Close()

		if requests != 1 || i.backoff != 0 || body != nil {
			t.Errorf("%d: asked %d times, waits %s and got %q, want one request and no wait", status, requests, i.backoff, body)
		}

		if wantError {
			want := fmt.Sprintf("%s answered %d", strings.TrimPrefix(server.URL, "http://"), status)
			if err == nil || err.Error() != want {
				t.Errorf("%d: got %v, want %q", status, err, want)
			}
		} else if err != nil {
			t.Errorf("%d: got %v, want nothing, as there is nothing there", status, err)
		}
	}
}

func TestRetryLeavesOutCoursesTriedTheMostTimesAllowed(t *testing.T) {
	s := newSetup(t)
	output := logged(t)

	s.importer.walk(context.Background(), "test", 100, 109, 1, 0)

	recorder := s.recordService(t, nil)

	// * Both courses that were not archived have been tried once
	s.importer.retry(context.Background(), 1, 0)

	if !strings.Contains(output.String(), "trying 0 courses again") {
		t.Errorf("did not leave out the courses tried once. Logged:\n%s", output)
	}

	listed := recorder.requests[0]
	if !strings.HasPrefix(listed, "GET "+importsvc.AttemptsPath+"?") {
		t.Fatalf("first asked for %q, want the attempts", listed)
	}

	query, _ := url.ParseQuery(listed[strings.Index(listed, "?")+1:])
	if query.Get("max_tries") != "1" || query.Get("limit") != "10000" || fmt.Sprint(query["outcome"]) != "[failed not_archived]" {
		t.Errorf("asked for attempts with %v, want at most 1 try, up to 10000, failed or not archived", query)
	}

	// * -count limits how many are tried
	s.importer.retry(context.Background(), 2, 1)

	attempts, _ := s.store.ImportAttemptsToRetry([]string{store.ImportNotArchived}, 5, 10)

	tries := map[uint64]int{}
	for _, attempt := range attempts {
		tries[attempt.DataID] = attempt.Tries
	}

	if len(tries) != 2 || tries[5003]+tries[5007] != 3 {
		t.Errorf("tries are %v, want one of the two courses tried again", tries)
	}
}

func TestRetrySkipsAnAttemptWhoseIndexEntryCanNotBeRead(t *testing.T) {
	s := newSetup(t)
	output := logged(t)

	for dataID, entry := range map[uint64]string{6000: `{"levelid":0}`, 6001: `{"levelid":"not a number"}`} {
		if err := s.store.RecordImportAttempt(store.ImportAttempt{DataID: dataID, Outcome: store.ImportFailed, Entry: []byte(entry)}); err != nil {
			t.Fatal(err)
		}
	}

	s.importer.retry(context.Background(), 3, 0)

	for _, want := range []string{"course 6000: its index entry could not be read", "course 6001: its index entry could not be read"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("did not log %q. Logged:\n%s", want, output)
		}
	}

	if len(s.archive.asked) != 0 {
		t.Errorf("asked the index for %v while retrying", s.archive.asked)
	}
}

// * What the setup's index says about the course at a position
func entryAt(position int64) indexEntry {
	id := uint64(5000 + position - 100)

	return indexEntry{
		URL:        fmt.Sprintf("https://cdn.example.com/data/%d", id),
		Name:       fmt.Sprintf("Course %d", id),
		LevelID:    id,
		UploadTime: "2016-01-02T03:04:05.000Z",
	}
}

func (s *setup) attempt(t *testing.T, dataID uint64, outcome string) *store.ImportAttempt {
	t.Helper()

	attempts, err := s.store.ImportAttemptsToRetry([]string{outcome}, 100, 100)
	if err != nil {
		t.Fatal(err)
	}

	for _, attempt := range attempts {
		if attempt.DataID == dataID {
			return &attempt
		}
	}

	return nil
}

func TestACourseWhoseIndexEntryCanNotBeUsedIsRefused(t *testing.T) {
	s := newSetup(t)
	logged(t)

	noAddress := entryAt(100)
	noAddress.URL = ""

	badTime := entryAt(101)
	badTime.UploadTime = "yesterday"

	s.importer.bringIn(context.Background(), noAddress)
	s.importer.bringIn(context.Background(), badTime)

	if s.importer.refused != 2 || s.onServer(5000) || s.onServer(5001) {
		t.Errorf("refused %d, want both courses refused and left out", s.importer.refused)
	}

	if attempt := s.attempt(t, 5000, store.ImportRefused); attempt == nil || attempt.Detail != "the index has no address for its file" {
		t.Errorf("kept %+v for the course without an address", attempt)
	}

	if attempt := s.attempt(t, 5001, store.ImportRefused); attempt == nil || !strings.HasPrefix(attempt.Detail, `the index entry can not be used: upload time "yesterday": `) {
		t.Errorf("kept %+v for the course with a bad upload time", attempt)
	}
}

func TestAPageInPlaceOfACourseFileIsNotArchived(t *testing.T) {
	s := newSetup(t)
	s.archive.file = []byte("<html>the Wayback Machine has nothing here</html>")

	s.importer.bringIn(context.Background(), entryAt(100))

	if s.importer.notArchived != 1 || s.onServer(5000) {
		t.Errorf("not archived %d, want the page taken as no copy", s.importer.notArchived)
	}

	if attempt := s.attempt(t, 5000, store.ImportNotArchived); attempt == nil || attempt.Detail != "the Wayback Machine has no copy of the file" {
		t.Errorf("kept %+v", attempt)
	}
}

func TestAFileTheImportServiceRefusesIsKeptAsRefused(t *testing.T) {
	s := newSetup(t)
	logged(t)
	s.archive.file = []byte("ASH0 but not a course")

	s.importer.bringIn(context.Background(), entryAt(100))

	if s.importer.refused != 1 || s.onServer(5000) {
		t.Errorf("refused %d, want the broken file refused", s.importer.refused)
	}

	// * Kept with why the import service refused it
	if attempt := s.attempt(t, 5000, store.ImportRefused); attempt == nil || attempt.Detail == "" {
		t.Errorf("kept %+v, want the import service's answer", attempt)
	}
}

func TestACourseTheImportServiceCanNotTakeIsKeptAsFailed(t *testing.T) {
	s := newSetup(t)
	output := logged(t)

	s.recordService(t, func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == importsvc.Path
	})

	s.importer.bringIn(context.Background(), entryAt(100))

	if s.importer.failed != 1 || s.onServer(5000) {
		t.Errorf("failed %d, want the course failed", s.importer.failed)
	}

	attempt := s.attempt(t, 5000, store.ImportFailed)
	if attempt == nil || attempt.Detail != "the import service answered 500: not now" {
		t.Errorf("kept %+v", attempt)
	}

	if !strings.Contains(output.String(), "course 5000 failed: the import service answered 500: not now") {
		t.Errorf("did not log the failure. Logged:\n%s", output)
	}
}

func TestACourseIsFailedWhenTheImportServiceCanNotBeReached(t *testing.T) {
	s := newSetup(t)
	output := logged(t)

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	s.importer.service = closed.URL

	s.importer.bringIn(context.Background(), entryAt(100))

	if s.importer.failed != 1 {
		t.Errorf("failed %d, want 1", s.importer.failed)
	}

	for _, want := range []string{"course 5000 failed: the import service could not be reached", "course 5000: the attempt could not be recorded: the import service could not be reached"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("did not log %q. Logged:\n%s", want, output)
		}
	}
}

func TestACourseAlreadyOnTheServerIsNotFetchedAgain(t *testing.T) {
	s := newSetup(t)

	s.importer.walk(context.Background(), "first", 100, 109, 1, 0)

	s.importer.imported = 0
	s.archive.mutex.Lock()
	s.archive.file = []byte("not fetched again, or it would be refused")
	s.archive.mutex.Unlock()

	s.importer.walk(context.Background(), "second", 100, 109, 1, 0)

	if s.importer.alreadyThere != 8 || s.importer.imported != 0 || s.importer.refused != 0 {
		t.Errorf("already there %d, brought in %d, refused %d; want the 8 there found", s.importer.alreadyThere, s.importer.imported, s.importer.refused)
	}
}

func TestAnIndexEntryBecomesTheCourseTheImportServiceIsSent(t *testing.T) {
	entry := indexEntry{
		LevelID:         5000,
		Name:            "Course 5000",
		Creator:         "maker",
		CreatorID:       1700000000,
		CreatorMii:      "not base64!",
		Clears:          3,
		TotalAttempts:   10,
		Stars:           2,
		UploadTime:      "2016-01-02T03:04:05.000Z",
		RecordMS:        12345,
		RecordHolder:    "speedy",
		RecordHolderMii: base64.StdEncoding.EncodeToString([]byte("a mii")),
	}

	course, err := entry.course()
	if err != nil {
		t.Fatal(err)
	}

	if course.DataID != 5000 || course.Name != "Course 5000" || course.Stars != 2 || course.Clears != 3 || course.Attempts != 10 {
		t.Errorf("got %+v", course)
	}

	if !course.Uploaded.Equal(time.Date(2016, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("uploaded %s", course.Uploaded)
	}

	// * A Mii that can not be read is left out, for the service to stand in for
	if course.Maker.PID != 1700000000 || course.Maker.Name != "maker" || course.Maker.Mii != nil {
		t.Errorf("got maker %+v", course.Maker)
	}

	if course.Record == nil || course.Record.Milliseconds != 12345 || course.Record.Holder.Name != "speedy" || string(course.Record.Holder.Mii) != "a mii" {
		t.Errorf("got record %+v", course.Record)
	}

	// * A record needs both a time and who holds it
	for _, changed := range []indexEntry{{UploadTime: entry.UploadTime, RecordMS: 12345}, {UploadTime: entry.UploadTime, RecordHolder: "speedy"}} {
		if course, _ := changed.course(); course.Record != nil {
			t.Errorf("%+v gave a record", changed)
		}
	}
}
