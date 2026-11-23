package archivefetch

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
	"time"

	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/coursefile"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/seed"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

// * An archive of courses 2000000 to 2000009, at index positions 100 to
// * 109, and a Wayback Machine that has the files of some of them
const firstCourse = 2_000_000

type archive struct {
	mutex    sync.Mutex
	archived map[uint64]bool
	file     []byte
	byID     []uint64
	byPos    []int64
	files    []uint64
	// * Held until closed, when set, to keep a lookup waiting
	hold chan struct{}
	// * Answered instead of the course, when set
	status int
}

func (a *archive) entry(id uint64) map[string]any {
	return map[string]any{
		"url":            fmt.Sprintf("https://cdn.example.com/data/%d", id),
		"name":           fmt.Sprintf("Course %d", id),
		"creator":        "maker" + strconv.FormatUint(id, 10),
		"levelid":        id,
		"creatorid":      1700000000 + id%1000,
		"clears":         3,
		"total_attempts": 10,
		"uploadTime":     "2016-01-02T03:04:05.000Z",
		"stars":          2,
	}
}

func (a *archive) serve(w http.ResponseWriter, r *http.Request) {
	a.mutex.Lock()
	hold, status := a.hold, a.status
	a.mutex.Unlock()

	if hold != nil {
		<-hold
	}

	if status != 0 {
		w.WriteHeader(status)
		return
	}

	last := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	number, _ := strconv.ParseUint(last, 10, 64)

	a.mutex.Lock()
	defer a.mutex.Unlock()

	switch {
	case strings.HasPrefix(r.URL.Path, "/index/level/"):
		a.byID = append(a.byID, number)

		if number < firstCourse || number > firstCourse+9 {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"Level not found"}`))

			return
		}

		json.NewEncoder(w).Encode(a.entry(number))
	case strings.HasPrefix(r.URL.Path, "/index/searchRandomLevels/"):
		a.byPos = append(a.byPos, int64(number))

		if number < 100 || number > 109 {
			w.Write([]byte(`{"error":"No Random Level Found!"}`))
			return
		}

		json.NewEncoder(w).Encode([]map[string]any{a.entry(firstCourse + number - 100)})
	case strings.HasPrefix(r.URL.Path, "/wayback/"):
		a.files = append(a.files, number)

		if !a.archived[number] {
			http.NotFound(w, r)
			return
		}

		w.Write(a.file)
	default:
		http.NotFound(w, r)
	}
}

type setup struct {
	archive *archive
	store   store.Store
	fetcher *Fetcher
	put     map[string][]byte
	logged  []string
}

func newSetup(t *testing.T, change func(*Config)) *setup {
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
	for id := uint64(firstCourse); id <= firstCourse+9; id++ {
		a.archived[id] = id != firstCourse+3 && id != firstCourse+7
	}

	server := httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		a.mutex.Lock()
		if a.hold != nil {
			select {
			case <-a.hold:
			default:
				close(a.hold)
			}
		}
		a.mutex.Unlock()
	})

	s := &setup{archive: a, store: opened, put: map[string][]byte{}}
	var putMutex sync.Mutex
	var logMutex sync.Mutex

	config := Config{
		Seeder: &seed.Seeder{
			Store:   opened,
			KeyBase: "courses",
			PutFile: func(key string, data []byte) error {
				putMutex.Lock()
				s.put[key] = data
				putMutex.Unlock()

				return nil
			},
		},
		FillGap:   time.Millisecond,
		UrgentGap: time.Millisecond,
		HTTP:      server.Client(),
		Index:     server.URL + "/index/",
		Wayback:   server.URL + "/wayback/",
		Positions: [2]int64{100, 109},
		FillPause: time.Millisecond,
		Log: func(line string) {
			logMutex.Lock()
			s.logged = append(s.logged, line)
			logMutex.Unlock()
		},
	}

	if change != nil {
		change(&config)
	}

	s.fetcher, err = New(config)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func (s *setup) here(id uint64) bool {
	_, nexError := s.store.GetObjectInfoByDataID(types.UInt64(id))
	return nexError == nil
}

func (s *setup) asked() (byID []uint64, byPos []int64, files []uint64) {
	s.archive.mutex.Lock()
	defer s.archive.mutex.Unlock()

	return append([]uint64{}, s.archive.byID...), append([]int64{}, s.archive.byPos...), append([]uint64{}, s.archive.files...)
}

func TestACourseAPlayerAsksForIsBroughtInOnce(t *testing.T) {
	s := newSetup(t, nil)

	if !s.fetcher.Fetch(context.Background(), firstCourse+1) {
		t.Fatal("the course was not brought in")
	}

	if !s.here(firstCourse+1) || s.put[fmt.Sprintf("courses/%d.bin", firstCourse+1)] == nil {
		t.Fatalf("the course is not on the server with its file (files: %v)", keys(s.put))
	}

	// * Asked again, it is already here
	if !s.fetcher.Fetch(context.Background(), firstCourse+1) {
		t.Fatal("the course was not found the second time")
	}

	if byID, _, files := s.asked(); fmt.Sprint(byID) != fmt.Sprint([]uint64{firstCourse + 1}) || len(files) != 1 {
		t.Errorf("asked the index for %v and the Wayback Machine for %v, want each once", byID, files)
	}
}

func TestACourseNobodyArchivedIsNotAskedForAgainForAWhile(t *testing.T) {
	s := newSetup(t, nil)

	for range 3 {
		if s.fetcher.Fetch(context.Background(), firstCourse+3) {
			t.Fatal("a course with no archived file was brought in")
		}
	}

	if byID, _, files := s.asked(); len(byID) != 1 || len(files) != 1 {
		t.Errorf("asked the index %d times and the Wayback Machine %d times, want once each", len(byID), len(files))
	}

	attempts, err := s.store.ImportAttemptsToRetry([]string{store.ImportNotArchived}, 5, 10)
	if err != nil || len(attempts) != 1 || attempts[0].DataID != firstCourse+3 || len(attempts[0].Entry) == 0 {
		t.Errorf("kept %+v (%v), want the course kept as not archived, with its index entry", attempts, err)
	}
}

func TestAnIDTheIndexDoesNotKnowIsNotAskedForAgainForAWhile(t *testing.T) {
	s := newSetup(t, nil)

	for range 3 {
		if s.fetcher.Fetch(context.Background(), firstCourse+50) {
			t.Fatal("an unknown course was brought in")
		}
	}

	if byID, _, files := s.asked(); len(byID) != 1 || len(files) != 0 {
		t.Errorf("asked the index %d times and the Wayback Machine %d times, want once and never", len(byID), len(files))
	}
}

func TestOnlyIDsAnArchivedCourseCouldHaveAreLookedUp(t *testing.T) {
	s := newSetup(t, nil)

	for _, id := range []uint64{0, 900000, store.FirstDataID - 1, store.ImportedDataIDLimit, store.ImportedDataIDLimit + 5} {
		if s.fetcher.Fetch(context.Background(), id) {
			t.Errorf("ID %d was brought in", id)
		}
	}

	if byID, _, _ := s.asked(); len(byID) != 0 {
		t.Errorf("asked the index for %v, want nothing", byID)
	}
}

func TestPlayersAskingForTheSameCourseAtOnceShareOneFetch(t *testing.T) {
	s := newSetup(t, nil)

	var wg sync.WaitGroup
	results := make([]bool, 8)

	for i := range results {
		wg.Add(1)

		go func() {
			defer wg.Done()
			results[i] = s.fetcher.Fetch(context.Background(), firstCourse+2)
		}()
	}

	wg.Wait()

	for i, ok := range results {
		if !ok {
			t.Errorf("player %d was told the course is not there", i)
		}
	}

	if byID, _, files := s.asked(); len(byID) != 1 || len(files) != 1 {
		t.Errorf("asked the index %d times and the Wayback Machine %d times, want once each", len(byID), len(files))
	}
}

func TestAFetchThePlayerGaveUpOnCarriesOn(t *testing.T) {
	s := newSetup(t, nil)

	s.archive.mutex.Lock()
	s.archive.hold = make(chan struct{})
	s.archive.mutex.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if s.fetcher.Fetch(ctx, firstCourse+4) {
		t.Fatal("the course was said to be here before the archive answered")
	}

	s.archive.mutex.Lock()
	close(s.archive.hold)
	s.archive.hold = nil
	s.archive.mutex.Unlock()

	// * The next ask waits on the same fetch, which now finishes
	if !s.fetcher.Fetch(context.Background(), firstCourse+4) {
		t.Fatal("the course was not brought in after all")
	}

	if byID, _, _ := s.asked(); len(byID) != 1 {
		t.Errorf("asked the index %d times, want once", len(byID))
	}
}

func TestAnArchiveThatIsDownIsTriedAgainSoonAndSlowedDownFor(t *testing.T) {
	s := newSetup(t, func(config *Config) {
		config.RetryAfter = func(string) time.Duration { return 0 }
	})

	s.archive.mutex.Lock()
	s.archive.status = http.StatusServiceUnavailable
	s.archive.mutex.Unlock()

	if s.fetcher.Fetch(context.Background(), firstCourse+5) {
		t.Fatal("a course was brought in from an archive that is down")
	}

	s.fetcher.client.gate.mutex.Lock()
	backoff := s.fetcher.client.gate.backoff
	s.fetcher.client.gate.backoff = 0
	s.fetcher.client.gate.mutex.Unlock()

	if backoff < 30*time.Second {
		t.Errorf("waits %s more between requests, want at least 30s once told to slow down", backoff)
	}

	s.archive.mutex.Lock()
	s.archive.status = 0
	s.archive.mutex.Unlock()

	if !s.fetcher.Fetch(context.Background(), firstCourse+5) {
		t.Error("the course was not brought in once the archive was back")
	}
}

func TestTheFillBringsInRandomCoursesUpToItsLimit(t *testing.T) {
	position := int64(0)

	s := newSetup(t, func(config *Config) {
		config.FillLimit = 5
		// * Walks the positions in order, so the test knows which come
		config.RandomInt = func(n int64) int64 {
			defer func() { position++ }()
			return position % n
		}
	})

	done := make(chan struct{})

	go func() {
		s.fetcher.Fill(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the fill did not stop at its limit")
	}

	// * Positions 100 to 105 hold 2000000 to 2000005, of which 2000003
	// * has no file: 5 brought in after 6 positions, and no more
	for id := uint64(firstCourse); id <= firstCourse+6; id++ {
		if want := id != firstCourse+3 && id != firstCourse+6; s.here(id) != want {
			t.Errorf("course %d here: %v, want %v", id, s.here(id), want)
		}
	}

	if _, byPos, _ := s.asked(); fmt.Sprint(byPos) != "[100 101 102 103 104 105]" {
		t.Errorf("asked the index for positions %v, want 100 to 105", byPos)
	}

	if count, _ := s.store.ImportCursor(fillCounter); count != 5 {
		t.Errorf("the fill's count is %d, want 5", count)
	}

	// * Started again, it is already at its limit
	before, _, _ := s.asked()
	s.fetcher.Fill(context.Background())

	if after, _, _ := s.asked(); len(after) != len(before) {
		t.Error("a fill at its limit asked the archive again")
	}
}

func TestTheFillIsOffWithNoLimit(t *testing.T) {
	s := newSetup(t, nil)

	s.fetcher.Fill(context.Background())

	if _, byPos, _ := s.asked(); len(byPos) != 0 {
		t.Errorf("asked the index for positions %v, want none", byPos)
	}
}

func TestAPlayerWaitingGoesAheadOfTheFill(t *testing.T) {
	g := &gate{fillGap: time.Hour, urgentGap: time.Millisecond}

	if err := g.take(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	// * The fill's next request waits an hour; a player's does not
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := g.take(ctx, true); err != nil {
		t.Fatalf("a player's request waited: %v", err)
	}

	// * And the fill does not go while a player is waiting
	g.fillGap = 0
	g.waiting = 1

	short, cancelShort := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelShort()

	if err := g.take(short, false); err == nil {
		t.Error("the fill went ahead of a waiting player")
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}

	return out
}
