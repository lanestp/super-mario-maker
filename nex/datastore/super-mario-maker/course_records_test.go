package nex_datastore_super_mario_maker

import (
	"testing"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_super_mario_maker "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker"
	datastore_super_mario_maker_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
)

// * Course records are kept in slot 0, as a real game does
const courseRecordSlot = 0

// * Uploads a time on a course for a player, as a game does on a clear
func uploadRecord(t *testing.T, player types.PID, course types.UInt64, score int32) {
	t.Helper()

	param := datastore_super_mario_maker_types.NewDataStoreUploadCourseRecordParam()
	param.DataID = course
	param.Slot = courseRecordSlot
	param.Score = types.Int32(score)

	rmc, nexError := UploadCourseRecord(nil, from(player), 2, param)
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodUploadCourseRecord, 2))
}

func recordParam(course types.UInt64) datastore_super_mario_maker_types.DataStoreGetCourseRecordParam {
	param := datastore_super_mario_maker_types.NewDataStoreGetCourseRecordParam()
	param.DataID = course
	param.Slot = courseRecordSlot

	return param
}

func TestTheFastestClearIsTheRecordAndTheFirstClearIsKept(t *testing.T) {
	server := setUp(t)

	course := addObject(t, server.store, object{})

	// * A lower score is a faster time
	uploadRecord(t, 1000, course, 5000)
	uploadRecord(t, 2000, course, 4000)
	uploadRecord(t, 3000, course, 6000)

	rmc, nexError := GetCourseRecord(nil, from(4000), 6, recordParam(course))

	record := datastore_super_mario_maker_types.NewDataStoreGetCourseRecordResult()
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodGetCourseRecord, 6), &record)

	if record.DataID != course || record.Slot != courseRecordSlot {
		t.Errorf("got the record of %d slot %d, want %d slot %d", record.DataID, record.Slot, course, courseRecordSlot)
	}

	if record.FirstPID != 1000 || record.BestPID != 2000 || record.BestScore != 4000 {
		t.Errorf("got first %d, best %d with %d, want first 1000, best 2000 with 4000", record.FirstPID, record.BestPID, record.BestScore)
	}

	if record.CreatedTime.Standard().IsZero() || record.UpdatedTime.Standard().Before(record.CreatedTime.Standard()) {
		t.Errorf("made at %s and updated at %s, want a time made and an update no earlier", record.CreatedTime.Standard(), record.UpdatedTime.Standard())
	}
}

func TestACourseNobodyHasClearedHasNoRecord(t *testing.T) {
	server := setUp(t)

	course := addObject(t, server.store, object{})

	rmc, nexError := GetCourseRecord(nil, from(1000), 1, recordParam(course))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.NotFound)
}

func TestRecordsOfMissingCoursesAreNotFound(t *testing.T) {
	server := setUp(t)

	course := addObject(t, server.store, object{})
	uploadRecord(t, 1000, course, 100)
	deleteObject(t, server.store, course)

	rmc, nexError := GetCourseRecord(nil, from(1000), 1, recordParam(course))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.NotFound)

	param := datastore_super_mario_maker_types.NewDataStoreUploadCourseRecordParam()
	param.DataID = course
	param.Score = 50

	rmc, nexError = UploadCourseRecord(nil, from(2000), 1, param)
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.NotFound)
}

func metaParam(resultOption uint8) datastore_types.DataStoreGetMetaParam {
	param := datastore_types.NewDataStoreGetMetaParam()
	param.ResultOption = types.UInt8(resultOption)

	return param
}

// * Asks for the meta info and record of each course
func metasWithRecords(t *testing.T, player types.PID, resultOption uint8, courses ...types.UInt64) (types.List[datastore_types.DataStoreMetaInfo], types.List[datastore_super_mario_maker_types.DataStoreGetCourseRecordResult]) {
	t.Helper()

	params := make(types.List[datastore_super_mario_maker_types.DataStoreGetCourseRecordParam], 0, len(courses))
	for _, course := range courses {
		params = append(params, recordParam(course))
	}

	rmc, nexError := GetMetasWithCourseRecord(nil, from(player), 8, params, metaParam(resultOption))

	var metas types.List[datastore_types.DataStoreMetaInfo]
	var records types.List[datastore_super_mario_maker_types.DataStoreGetCourseRecordResult]
	var results types.List[types.QResult]
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodGetMetasWithCourseRecord, 8), &metas, &records, &results)

	// * The real server always says each one succeeded
	wantAllSucceeded(t, results, len(courses))

	if len(metas) != len(courses) || len(records) != len(courses) {
		t.Fatalf("got %d metas and %d records, want %d of each", len(metas), len(records), len(courses))
	}

	return metas, records
}

func TestMetasWithCourseRecordsGiveEachCourseAndItsRecord(t *testing.T) {
	server := setUp(t)

	course := addObject(t, server.store, object{name: "castle", tags: []string{"tag"}, metaBinary: []byte{1, 2}, ratingSlots: 5})
	uploadRecord(t, 2000, course, 900)

	uncleared := addObject(t, server.store, object{name: "uncleared"})

	metas, records := metasWithRecords(t, 3000, 0x7, course, uncleared)

	if metas[0].DataID != course || metas[0].Name != "castle" {
		t.Errorf("got meta %d %q, want %d castle", metas[0].DataID, metas[0].Name, course)
	}

	if len(metas[0].Tags) != 1 || len(metas[0].Ratings) != 5 || len(metas[0].MetaBinary) != 2 {
		t.Errorf("got %d tags, %d ratings and %d bytes of meta binary, want all of them as asked for", len(metas[0].Tags), len(metas[0].Ratings), len(metas[0].MetaBinary))
	}

	if records[0].DataID != course || records[0].BestPID != 2000 || records[0].BestScore != 900 {
		t.Errorf("got record %d by %d with %d, want %d by 2000 with 900", records[0].DataID, records[0].BestPID, records[0].BestScore, course)
	}

	if metas[1].DataID != uncleared || records[1].DataID != 0 {
		t.Errorf("the uncleared course gave meta %d and record %d, want its meta and an empty record", metas[1].DataID, records[1].DataID)
	}
}

func TestMetasWithCourseRecordsLeaveOutWhatWasNotAskedFor(t *testing.T) {
	server := setUp(t)

	course := addObject(t, server.store, object{tags: []string{"tag"}, metaBinary: []byte{1, 2}, ratingSlots: 5})

	for option, want := range map[uint8][3]int{
		0x0: {0, 0, 0},
		0x1: {1, 0, 0},
		0x2: {0, 5, 0},
		0x4: {0, 0, 2},
	} {
		metas, _ := metasWithRecords(t, 1000, option, course)

		if got := [3]int{len(metas[0].Tags), len(metas[0].Ratings), len(metas[0].MetaBinary)}; got != want {
			t.Errorf("result option 0x%X gave tags, ratings and meta binary of %v, want %v", option, got, want)
		}
	}
}

func TestMetasWithCourseRecordsAreBlankForCoursesThatCanNotBeSeen(t *testing.T) {
	server := setUp(t)

	// * Permission 1 is friends of the owner, 2 is the players listed,
	// * and 3 is the owner alone
	friendsOnly := addObject(t, server.store, object{owner: 1000, permission: 1})
	listed := addObject(t, server.store, object{owner: 1000, permission: 2, recipients: []types.PID{2000}})
	ownerOnly := addObject(t, server.store, object{owner: 1000, permission: 3})
	badPermission := addObject(t, server.store, object{owner: 1000, permission: 9})
	deleted := addObject(t, server.store, object{owner: 1000})

	for _, course := range []types.UInt64{friendsOnly, listed, ownerOnly, badPermission, deleted} {
		uploadRecord(t, 1000, course, 100)
	}

	deleteObject(t, server.store, deleted)
	server.friends[1000] = []uint32{2000}

	courses := []types.UInt64{friendsOnly, listed, ownerOnly, badPermission, deleted, deleted + 100}

	// * Player 2000 is a friend and is listed, so sees the first two
	metas, records := metasWithRecords(t, 2000, 0x7, courses...)
	for i, course := range courses {
		visible := i < 2

		if visible && (metas[i].DataID != course || records[i].BestScore != 100) {
			t.Errorf("course %d gave meta %d and record %d, want both", course, metas[i].DataID, records[i].BestScore)
		}

		if !visible && (metas[i].DataID != 0 || records[i].DataID != 0) {
			t.Errorf("course %d gave meta %d and record %d, want both blank", course, metas[i].DataID, records[i].DataID)
		}
	}

	// * Player 3000 is neither, and sees none of them
	metas, records = metasWithRecords(t, 3000, 0x7, courses[:3]...)
	for i := range 3 {
		if metas[i].DataID != 0 || records[i].DataID != 0 {
			t.Errorf("course %d was seen by a stranger", courses[i])
		}
	}

	// * The owner sees each course of theirs that still exists
	metas, _ = metasWithRecords(t, 1000, 0x7, courses[:3]...)
	for i := range 3 {
		if metas[i].DataID != courses[i] {
			t.Errorf("the owner could not see their course %d", courses[i])
		}
	}
}

func TestMetasWithCourseRecordsForNothingAreEmpty(t *testing.T) {
	setUp(t)

	metasWithRecords(t, 1000, 0x7)
}
