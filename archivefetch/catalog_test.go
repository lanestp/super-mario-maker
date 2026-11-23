package archivefetch

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

// * The archive's course database, as published, with courses 2000000 to
// * 2000009 (more played the higher the ID), an event course, a course
// * the index has no address for, and one with no record
func newCatalog(t *testing.T) *Catalog {
	t.Helper()

	path := filepath.Join(t.TempDir(), "smm_data.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	stamp := types.NewDateTime(0)
	uploaded := int64(stamp.FromTimestamp(time.Date(2016, 1, 2, 3, 4, 5, 0, time.UTC)))

	statements := []string{
		`CREATE TABLE Course (course_id INTEGER PRIMARY KEY, is_event_course BOOLEAN NOT NULL, is_official_maker_course BOOLEAN NOT NULL,
			upload_time INTEGER NOT NULL, course_name TEXT NOT NULL, creator_pid INTEGER NOT NULL, user_plays INTEGER NOT NULL,
			clears INTEGER NOT NULL, total_attempts INTEGER NOT NULL, failures INTEGER NOT NULL, stars INTEGER)`,
		`CREATE TABLE Player (player_id INTEGER PRIMARY KEY, nnid TEXT NOT NULL, mii_data TEXT NOT NULL, UNIQUE (nnid))`,
		`CREATE TABLE WorldRecord (course_id INTEGER PRIMARY KEY, best_time_pid INTEGER, first_complete_pid INTEGER,
			time_milliseconds INTEGER, created_time INTEGER, updated_time INTEGER)`,
		`INSERT INTO Player VALUES (1700000001, 'maker', 'QlBGQw=='), (1700000002, 'speedy', 'QlBGQwAB')`,
		`INSERT INTO Course VALUES (930001, 1, 0, 0, 'Event', 1700000001, 999999, 1, 1, 0, 1)`,
		`INSERT INTO Course VALUES (2000050, 0, 0, 0, 'Nowhere', 1700000001, 1, 0, 1, 1, 0)`,
	}

	for id := 0; id < 10; id++ {
		statements = append(statements, fmt.Sprintf(`INSERT INTO Course VALUES (%d, 0, 0, %d, 'Course %d', 1700000001, %d, 3, 10, 7, 2)`, firstCourse+id, uploaded, id, 100+id))

		if id != 9 {
			statements = append(statements, fmt.Sprintf(`INSERT INTO WorldRecord VALUES (%d, 1700000002, 1700000002, %d, 0, 0)`, firstCourse+id, 5000+id))
		}
	}

	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	catalog, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { catalog.Close() })

	return catalog
}

func TestTheCatalogDescribesACourseAsTheIndexDoes(t *testing.T) {
	catalog := newCatalog(t)

	entry, err := catalog.Course(firstCourse + 4)
	if err != nil || entry == nil {
		t.Fatalf("got %v (%v), want course %d", entry, err, firstCourse+4)
	}

	want := Entry{
		Name: "Course 4", Creator: "maker", LevelID: firstCourse + 4, CreatorID: 1700000001, CreatorMii: "QlBGQw==",
		Clears: 3, TotalAttempts: 10, UserPlays: 104, Stars: 2, UploadTime: "2016-01-02T03:04:05Z",
		RecordMS: 5004, RecordHolder: "speedy", RecordHolderPID: 1700000002, RecordHolderMii: "QlBGQwAB",
	}

	if *entry != want {
		t.Errorf("got\n%+v\nwant\n%+v", *entry, want)
	}

	if noRecord, _ := catalog.Course(firstCourse + 9); noRecord == nil || noRecord.RecordMS != 0 || noRecord.RecordHolder != "" {
		t.Errorf("a course with no record came back as %+v", noRecord)
	}

	for _, id := range []uint64{930001, 12345678} {
		if entry, err := catalog.Course(id); entry != nil || err != nil {
			t.Errorf("course %d: got %+v (%v), want nothing", id, entry, err)
		}
	}
}

func TestTheCatalogListsTheMostPlayedFirst(t *testing.T) {
	catalog := newCatalog(t)

	ids, err := catalog.MostPlayed(0, 3)
	if err != nil || fmt.Sprint(ids) != fmt.Sprint([]uint64{firstCourse + 9, firstCourse + 8, firstCourse + 7}) {
		t.Errorf("got %v (%v), want the three most played, leaving out the event course", ids, err)
	}

	ids, _ = catalog.MostPlayed(9, 5)
	if fmt.Sprint(ids) != fmt.Sprint([]uint64{firstCourse, 2000050}) {
		t.Errorf("past the first 9 got %v, want the last two", ids)
	}
}

func TestOpeningSomethingThatIsNotTheCatalogFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")

	db, _ := sql.Open("sqlite", path)
	db.Exec(`CREATE TABLE Other (x INTEGER)`)
	db.Close()

	if _, err := OpenCatalog(path); err == nil {
		t.Error("a database with no courses was taken as the catalog")
	}
}

func TestWithACatalogACourseIsDescribedFromIt(t *testing.T) {
	catalog := newCatalog(t)
	s := newSetup(t, func(config *Config) { config.Catalog = catalog })

	if !s.fetcher.Fetch(context.Background(), firstCourse+4) {
		t.Fatal("the course was not brought in")
	}

	// * The plays are the catalog's: the fake index has no play counts
	if plays := ratingOf(t, s.store, firstCourse+4, store.RatingSlotPlays); plays != 104 {
		t.Errorf("the course has %d plays, want the catalog's 104", plays)
	}

	record, nexError := s.store.GetCourseRecordByDataIDAndSlot(types.UInt64(firstCourse+4), 0)
	if nexError != nil || record.BestScore != 5004 || record.BestPID != 1700000002 {
		t.Errorf("the record is %+v (%v), want 5004 ms by the holder's own PID", record, nexError)
	}
}

func TestWithACatalogAnIDItDoesNotHaveIsNotLookedUp(t *testing.T) {
	catalog := newCatalog(t)
	s := newSetup(t, func(config *Config) { config.Catalog = catalog })

	if s.fetcher.Fetch(context.Background(), firstCourse+500) {
		t.Fatal("a course not in the catalog was brought in")
	}

	if byID, _, files := s.asked(); len(byID) != 0 || len(files) != 0 {
		t.Errorf("asked the index for %v and the Wayback Machine for %v, want nothing", byID, files)
	}

	// * In the catalog, but the index has no address for its file
	if s.fetcher.Fetch(context.Background(), 2000050) {
		t.Fatal("a course with no address for its file was brought in")
	}
}

func TestWithACatalogTheFillBringsInTheMostPlayedFirst(t *testing.T) {
	catalog := newCatalog(t)
	s := newSetup(t, func(config *Config) {
		config.Catalog = catalog
		config.FillLimit = 4
	})

	s.fetcher.Fill(context.Background())

	// * Most played first: 2000009, 2000008, 2000006 and 2000005, as
	// * 2000007 has no file
	for id := uint64(firstCourse); id <= firstCourse+9; id++ {
		want := id >= firstCourse+5 && id != firstCourse+7
		if s.here(id) != want {
			t.Errorf("course %d here: %v, want %v", id, s.here(id), want)
		}
	}

	// * It stops on the course that reached its limit, which it skips
	// * as already here when it carries on
	if rank, _ := s.store.ImportCursor(fillRank); rank != 4 {
		t.Errorf("the fill is %d down the most played, want 4", rank)
	}

	// * Started again with a higher limit, it carries on from there
	s.fetcher.config.FillLimit = 5
	s.fetcher.Fill(context.Background())

	if !s.here(firstCourse+4) || s.here(firstCourse+3) {
		t.Error("the fill did not carry on with the next most played")
	}
}

func TestTheFillTriesACourseAgainWhenTheArchiveIsDown(t *testing.T) {
	catalog := newCatalog(t)
	s := newSetup(t, func(config *Config) {
		config.Catalog = catalog
		config.FillLimit = 1
	})

	s.archive.mutex.Lock()
	s.archive.status = http.StatusServiceUnavailable
	s.archive.mutex.Unlock()

	tries := 0
	go func() {
		// * Back up after the first try
		for {
			s.archive.mutex.Lock()
			asked := s.archive.requests
			if asked > 0 {
				s.archive.status = 0
			}
			s.archive.mutex.Unlock()

			if asked > 0 {
				tries = asked
				return
			}

			time.Sleep(time.Millisecond)
		}
	}()

	// * The gate's slow-down is waited out quickly in a test
	go func() {
		for range 200 {
			s.fetcher.client.gate.mutex.Lock()
			s.fetcher.client.gate.backoff = 0
			s.fetcher.client.gate.mutex.Unlock()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	done := make(chan struct{})
	go func() {
		s.fetcher.Fill(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the fill did not finish")
	}

	if !s.here(firstCourse + 9) {
		t.Errorf("the most played course was skipped after %d tries while the archive was down, want it tried again", tries)
	}
}

func ratingOf(t *testing.T, s store.Store, dataID uint64, slot int8) int64 {
	t.Helper()

	info, nexError := s.GetObjectInfoByDataID(types.UInt64(dataID))
	if nexError != nil {
		t.Fatal(nexError)
	}

	for _, rating := range info.Ratings {
		if int8(rating.Slot) == slot {
			return int64(rating.Rating.TotalValue)
		}
	}

	return 0
}
