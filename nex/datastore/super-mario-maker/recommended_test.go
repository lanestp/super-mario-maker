package nex_datastore_super_mario_maker

import (
	"path/filepath"
	"testing"

	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

// * Makes a course tried attempts times and cleared clears times
func addCourse(t *testing.T, s store.Store, attempts, clears int32) types.UInt64 {
	t.Helper()

	param := datastore_types.NewDataStorePreparePostParam()
	param.DataType = 9

	id, nexError := s.InitializeObjectByPreparePostParam(types.PID(1000), param)
	if nexError != nil {
		t.Fatal(nexError)
	}

	dataID := types.UInt64(id)

	for slot := range 5 {
		rating := datastore_types.NewDataStoreRatingInitParamWithSlot()
		rating.Slot = types.Int8(slot)

		if nexError := s.InitializeObjectRatingWithSlot(id, rating); nexError != nil {
			t.Fatal(nexError)
		}
	}

	if nexError := s.UpdateObjectUploadCompletedByDataID(dataID, true); nexError != nil {
		t.Fatal(nexError)
	}

	if nexError := s.InsertOrUpdateCustomRanking(dataID, 0, 0); nexError != nil {
		t.Fatal(nexError)
	}

	for slot, total := range map[uint8]int32{store.RatingSlotAttempts: attempts, store.RatingSlotClears: clears} {
		if total > 0 {
			if _, nexError := s.RateObjectWithPassword(dataID, types.UInt8(slot), types.Int32(total), 0); nexError != nil {
				t.Fatal(nexError)
			}
		}
	}

	return dataID
}

func TestRecommendedCoursesPutTheRightDifficultyFirstAndNeverComeUpShort(t *testing.T) {
	globals.Logger = plogger.NewLogger()

	opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer opened.Close()

	previous := globals.Store
	globals.Store = opened
	t.Cleanup(func() { globals.Store = previous })

	easy := map[types.UInt64]bool{}
	for range 3 {
		easy[addCourse(t, opened, 20, 19)] = true
	}

	untried := addCourse(t, opened, 0, 0)

	for range 10 {
		addCourse(t, opened, 50, 1)
	}

	courses, nexError := recommendedCourses(8, extra("1", "0", "34", "", "0"))
	if nexError != nil {
		t.Fatal(nexError)
	}

	if len(courses) != 8 {
		t.Fatalf("got %d courses, want the 8 asked for", len(courses))
	}

	seen := map[types.UInt64]bool{}
	for i, course := range courses {
		id := course.MetaInfo.DataID

		if seen[id] {
			t.Errorf("course %d was sent twice", id)
		}

		seen[id] = true

		// * Easy ones first, then the untried one, then the rest
		switch {
		case i < 3 && !easy[id]:
			t.Errorf("course %d is in place %d, where the easy courses go", id, i)
		case i == 3 && id != untried:
			t.Errorf("place 3 holds course %d, want the course not tried yet", id)
		}
	}

	// * Asking for any difficulty takes no part in this
	if all, nexError := recommendedCourses(14, extra("", "", "", "0", "0")); nexError != nil || len(all) != 14 {
		t.Errorf("asking for any difficulty gave %d courses (%v), want all 14", len(all), nexError)
	}
}
