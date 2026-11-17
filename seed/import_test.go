package seed_test

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	miidata "github.com/PretendoNetwork/super-mario-maker/mii"
	"github.com/PretendoNetwork/super-mario-maker/seed"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

func archivedCourse(t *testing.T) seed.ArchivedCourse {
	t.Helper()

	makerMeta, _ := hex.DecodeString(realMakerMetaBinary)

	return seed.ArchivedCourse{
		DataID:   37585837,
		File:     courseFile(t, "Leap Of Faith"),
		Name:     "Leap Of Faith",
		Uploaded: time.Date(2016, 1, 2, 3, 4, 5, 0, time.UTC),
		Maker:    seed.ArchivedPlayer{PID: 1767174539, Name: "Sharkboy55555", MakerMetaBinary: makerMeta},
		Stars:    12,
		Clears:   51,
		Attempts: 464,
		Record:   &seed.ArchivedRecord{Milliseconds: 42911, Holder: seed.ArchivedPlayer{Name: "speedy"}},
	}
}

func makerObject(t *testing.T, s store.Store, pid types.PID) (datastore_types.DataStoreMetaInfo, bool) {
	t.Helper()

	target := datastore_types.NewDataStorePersistenceTarget()
	target.OwnerID = pid

	info, nexError := s.GetObjectInfoByPersistenceTargetWithPassword(target, 0)

	return info, nexError == nil
}

func TestImportCourseKeepsItsIDAndBringsEverythingAGameShows(t *testing.T) {
	s := newSetup(t)
	course := archivedCourse(t)

	if err := s.seeder.ImportCourse(course); err != nil {
		t.Fatal(err)
	}

	info, nexError := s.store.GetObjectInfoByDataID(37585837)
	if nexError != nil {
		t.Fatalf("the course is not available under its own ID: %v", nexError)
	}

	if info.Name != "Leap Of Faith" || info.OwnerID != 1767174539 || info.DataType != 9 || !info.CreatedTime.Standard().Equal(course.Uploaded) {
		t.Errorf("stored as %q by %d, type %d, made %s", info.Name, info.OwnerID, info.DataType, info.CreatedTime.Standard())
	}

	if len(info.MetaBinary) != 44 {
		t.Errorf("the meta binary is %d bytes, want the 44 a game reads the file with", len(info.MetaBinary))
	}

	if string(s.files["courses/37585837.bin"]) != string(course.File) {
		t.Error("the file is not stored under the course's ID")
	}

	totals := map[int8]int64{}
	for _, rating := range info.Ratings {
		totals[int8(rating.Slot)] = int64(rating.Rating.TotalValue)
	}

	if totals[store.RatingSlotClears] != 51 || totals[store.RatingSlotAttempts] != 464 || totals[store.RatingSlotFails] != 413 {
		t.Errorf("the rating slots hold %v", totals)
	}

	// * 413 of 464 attempts failed is 89%, which is Expert
	expert, nexError := s.store.GetRandomCoursesByFailureRate(10, store.FailureRateFilter{MinPercent: 75, MaxPercent: 95, MinAttempts: 10})
	if nexError != nil || len(expert) != 1 || expert[0].MetaInfo.DataID != 37585837 || expert[0].Score != 12 {
		t.Errorf("Expert courses are %v (%v), want this one with 12 stars", expert, nexError)
	}

	maker, found := makerObject(t, s.store, 1767174539)
	if !found || maker.Name != "Sharkboy55555" || string(maker.MetaBinary) != string(course.Maker.MakerMetaBinary) {
		t.Errorf("the maker object is %+v (found %v), want one with their own Mii", maker, found)
	}

	if rankings := s.store.GetCustomRankingsByDataIDs(300000000, types.List[types.UInt64]{maker.DataID}); len(rankings) != 1 || rankings[0].Score != 12 {
		t.Errorf("the maker's star ranking is %v, want 12", rankings)
	}

	record, nexError := s.store.GetCourseRecordByDataIDAndSlot(37585837, 0)
	if nexError != nil || record.BestScore != 42911 {
		t.Fatalf("the record is %+v (%v), want 42911 ms", record, nexError)
	}

	holder, found := makerObject(t, s.store, record.BestPID)
	if !found || holder.Name != "speedy" {
		t.Errorf("the record holder %d has maker object %+v (found %v)", record.BestPID, holder, found)
	}

	if record.BestPID < 1_500_000_000 || record.BestPID >= 1_600_000_000 {
		t.Errorf("the record holder got PID %d, outside the range kept for made up PIDs", record.BestPID)
	}
}

func TestImportCourseTwiceKeepsTheFirst(t *testing.T) {
	s := newSetup(t)
	course := archivedCourse(t)

	if err := s.seeder.ImportCourse(course); err != nil {
		t.Fatal(err)
	}

	course.Name = "Renamed"
	course.Stars = 999

	if err := s.seeder.ImportCourse(course); !errors.Is(err, seed.ErrAlreadyImported) {
		t.Fatalf("got %v, want ErrAlreadyImported", err)
	}

	if info, _ := s.store.GetObjectInfoByDataID(37585837); info.Name != "Leap Of Faith" {
		t.Errorf("the course was changed to %q", info.Name)
	}

	// * A maker of two courses has one maker object, with both courses' stars
	other := archivedCourse(t)
	other.DataID = 37585838
	other.Stars = 3

	if err := s.seeder.ImportCourse(other); err != nil {
		t.Fatal(err)
	}

	maker, _ := makerObject(t, s.store, 1767174539)
	if rankings := s.store.GetCustomRankingsByDataIDs(300000000, types.List[types.UInt64]{maker.DataID}); len(rankings) != 1 || rankings[0].Score != 15 {
		t.Errorf("the maker's stars are %v, want 12 and 3 together", rankings)
	}
}

func TestImportCourseRefusesBadCoursesWithoutWritingAnything(t *testing.T) {
	for name, change := range map[string]func(*seed.ArchivedCourse){
		"a file that is not a course": func(c *seed.ArchivedCourse) { c.File = []byte("not a course") },
		"more clears than attempts":   func(c *seed.ArchivedCourse) { c.Clears = c.Attempts + 1 },
		"no DataID":                   func(c *seed.ArchivedCourse) { c.DataID = 0 },
		"a maker with no PID":         func(c *seed.ArchivedCourse) { c.Maker.PID = 0 },
		"a name that is too long":     func(c *seed.ArchivedCourse) { c.Name = "this name is far too long to be a course name" },
		"a record with no time":       func(c *seed.ArchivedCourse) { c.Record.Milliseconds = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			s := newSetup(t)
			course := archivedCourse(t)
			change(&course)

			if err := s.seeder.ImportCourse(course); !errors.Is(err, seed.ErrInvalidCourse) {
				t.Fatalf("got %v, want ErrInvalidCourse", err)
			}

			if len(s.files) != 0 {
				t.Error("a file was stored")
			}

			if _, found := makerObject(t, s.store, 1767174539); found {
				t.Error("a maker object was made")
			}
		})
	}
}

func TestImportCourseGivesAMakerWithNoUsableMiiAStandIn(t *testing.T) {
	s := newSetup(t)
	course := archivedCourse(t)
	course.Maker.MakerMetaBinary = []byte("BPFC but nothing else")

	if err := s.seeder.ImportCourse(course); err != nil {
		t.Fatal(err)
	}

	maker, _ := makerObject(t, s.store, 1767174539)
	held := []byte(maker.MetaBinary)[0x18 : 0x18+miidata.Size]

	if binary.BigEndian.Uint16(held[94:]) != miidata.Checksum(held[:94]) {
		t.Error("the stand-in Mii has a wrong checksum, so a game would refuse it")
	}
}
