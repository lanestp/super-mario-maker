// Package storetest holds the tests every storage backend must pass
package storetest

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_smm_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

// Backend is a storage backend to test
type Backend struct {
	// Open returns a new, empty store. It is closed by the test
	Open func(t *testing.T) store.Store

	// SetUnderReview marks an object as under review, which
	// no method of store.Store does. It may use the last opened store
	SetUnderReview func(t *testing.T, dataID uint64)
}

// Run runs every storage test against the backend
func Run(t *testing.T, backend Backend) {
	if globals.Logger == nil {
		globals.Logger = plogger.NewLogger()
	}

	tests := []struct {
		name string
		run  func(t *testing.T, s store.Store, backend Backend)
	}{
		{"ObjectLifecycle", testObjectLifecycle},
		{"ObjectFields", testObjectFields},
		{"AttachFile", testAttachFile},
		{"ObjectUpdates", testObjectUpdates},
		{"PersistenceTarget", testPersistenceTarget},
		{"Ratings", testRatings},
		{"CustomRankings", testCustomRankings},
		{"CourseRecords", testCourseRecords},
		{"BufferQueues", testBufferQueues},
		{"UserCourseObjectIDs", testUserCourseObjectIDs},
		{"RandomCourses", testRandomCourses},
		{"RandomCoursesSkipUnavailable", testRandomCoursesSkipUnavailable},
		{"UnderReview", testUnderReview},
		{"EventCourseMetadata", testEventCourseMetadata},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := backend.Open(t)
			t.Cleanup(func() { s.Close() })

			test.run(t, s, backend)
		})
	}
}

// * DataType 6 is one of the course data types
const courseDataType = 6

type objectOptions struct {
	owner             uint64
	name              string
	dataType          uint16
	persistenceSlotID uint16
	ratingSlots       int
	incomplete        bool
}

func createObject(t *testing.T, s store.Store, options objectOptions) types.UInt64 {
	t.Helper()

	param := datastore_types.NewDataStorePreparePostParam()
	param.Size = 1024
	param.Name = types.String(options.name)
	param.DataType = types.UInt16(options.dataType)
	param.MetaBinary = types.QBuffer{0xAB, 0xCD}
	param.Period = 90
	param.PersistenceInitParam.PersistenceSlotID = types.UInt16(options.persistenceSlotID)

	dataID, nexError := s.InitializeObjectByPreparePostParam(types.PID(options.owner), param)
	mustSucceed(t, nexError)

	for slot := 0; slot < options.ratingSlots; slot++ {
		rating := datastore_types.NewDataStoreRatingInitParamWithSlot()
		rating.Slot = types.Int8(slot)

		mustSucceed(t, s.InitializeObjectRatingWithSlot(dataID, rating))
	}

	if !options.incomplete {
		mustSucceed(t, s.UpdateObjectUploadCompletedByDataID(types.UInt64(dataID), true))
	}

	return types.UInt64(dataID)
}

// * Creates an available course which shows up in random course searches
func createCourse(t *testing.T, s store.Store, owner uint64, name string) types.UInt64 {
	t.Helper()

	dataID := createObject(t, s, objectOptions{owner: owner, name: name, dataType: courseDataType, ratingSlots: 2})
	mustSucceed(t, s.InsertOrUpdateCustomRanking(dataID, 0, 0))

	return dataID
}

func mustSucceed(t *testing.T, nexError *nex.Error) {
	t.Helper()

	if nexError != nil {
		t.Fatalf("unexpected error: %v", nexError)
	}
}

func mustFail(t *testing.T, nexError *nex.Error, resultCode uint32, what string) {
	t.Helper()

	if nexError == nil {
		t.Fatalf("%s: got no error, want result code %#x", what, resultCode)
	}

	// * nex.NewError sets the top bit of the result code to mark it as an error
	const errorMask = 1 << 31

	if nexError.ResultCode&^errorMask != resultCode&^errorMask {
		t.Fatalf("%s: got result code %#x (%v), want %#x", what, nexError.ResultCode, nexError, resultCode)
	}
}

func testObjectLifecycle(t *testing.T, s store.Store, _ Backend) {
	first := createObject(t, s, objectOptions{owner: 1000, name: "first", dataType: courseDataType, incomplete: true})
	if first != store.FirstDataID {
		t.Errorf("first object got DataID %d, want %d", first, store.FirstDataID)
	}

	mustFail(t, s.IsObjectAvailable(first), nex.ResultCodes.DataStore.NotFound, "object before its upload completes")

	_, nexError := s.GetObjectInfoByDataID(first)
	mustFail(t, nexError, nex.ResultCodes.DataStore.NotFound, "info of object before its upload completes")

	mustSucceed(t, s.UpdateObjectUploadCompletedByDataID(first, true))
	mustSucceed(t, s.IsObjectAvailable(first))
	mustSucceed(t, s.IsObjectAvailableWithPassword(first, 0))

	second := createObject(t, s, objectOptions{owner: 1000, name: "second", dataType: courseDataType})
	if second != first+1 {
		t.Errorf("second object got DataID %d, want %d", second, first+1)
	}

	mustSucceed(t, s.DeleteObjectByDataID(first))
	mustFail(t, s.IsObjectAvailable(first), nex.ResultCodes.DataStore.NotFound, "deleted object")
	mustFail(t, s.DeleteObjectByDataID(first), nex.ResultCodes.DataStore.NotFound, "deleting a deleted object")
	mustSucceed(t, s.IsObjectAvailable(second))

	mustFail(t, s.IsObjectAvailable(12345), nex.ResultCodes.DataStore.NotFound, "object which was never created")
	mustFail(t, s.UpdateObjectUploadCompletedByDataID(12345, true), nex.ResultCodes.DataStore.NotFound, "completing an object which was never created")
}

func testObjectFields(t *testing.T, s store.Store, _ Backend) {
	param := datastore_types.NewDataStorePreparePostParam()
	param.Size = 4321
	param.Name = "My Course"
	param.DataType = courseDataType
	param.MetaBinary = types.QBuffer{1, 2, 3, 4, 5}
	param.Permission.Permission = 2
	param.Permission.RecipientIDs = types.List[types.PID]{2001, 2002}
	param.DelPermission.Permission = 3
	param.Flag = 256
	param.Period = 365
	param.ReferDataID = 77
	// * More tags than extra data, and the reverse, must both be accepted
	param.Tags = types.List[types.String]{"one", "two"}
	param.ExtraData = types.List[types.String]{"a", "b", "c"}

	created, nexError := s.InitializeObjectByPreparePostParam(1000, param)
	mustSucceed(t, nexError)

	dataID := types.UInt64(created)
	mustSucceed(t, s.UpdateObjectUploadCompletedByDataID(dataID, true))

	param.Tags = types.List[types.String]{"one", "two", "three"}
	param.ExtraData = types.List[types.String]{}

	_, nexError = s.InitializeObjectByPreparePostParam(1000, param)
	mustSucceed(t, nexError)

	info, nexError := s.GetObjectInfoByDataID(dataID)
	mustSucceed(t, nexError)

	if info.DataID != dataID || info.OwnerID != 1000 || info.Size != 4321 || info.Name != "My Course" || info.DataType != courseDataType {
		t.Errorf("got DataID %d, owner %d, size %d, name %q, data type %d", info.DataID, info.OwnerID, info.Size, info.Name, info.DataType)
	}

	if !bytes.Equal(info.MetaBinary, []byte{1, 2, 3, 4, 5}) {
		t.Errorf("got meta binary %v", info.MetaBinary)
	}

	if info.Permission.Permission != 2 || len(info.Permission.RecipientIDs) != 2 || info.Permission.RecipientIDs[0] != 2001 || info.Permission.RecipientIDs[1] != 2002 {
		t.Errorf("got permission %d with recipients %v", info.Permission.Permission, info.Permission.RecipientIDs)
	}

	if info.DelPermission.Permission != 3 || len(info.DelPermission.RecipientIDs) != 0 {
		t.Errorf("got delete permission %d with recipients %v", info.DelPermission.Permission, info.DelPermission.RecipientIDs)
	}

	if info.Flag != 256 || info.Period != 365 || info.ReferDataID != 77 {
		t.Errorf("got flag %d, period %d, refer DataID %d", info.Flag, info.Period, info.ReferDataID)
	}

	if len(info.Tags) != 2 || info.Tags[0] != "one" || info.Tags[1] != "two" {
		t.Errorf("got tags %v", info.Tags)
	}

	if info.CreatedTime == 0 || info.UpdatedTime == 0 || info.ReferredTime != info.CreatedTime {
		t.Errorf("got created time %d, updated time %d, referred time %d", info.CreatedTime, info.UpdatedTime, info.ReferredTime)
	}

	if info.ExpireTime != 0x9C3F3E0000 {
		t.Errorf("got expire time %#x", uint64(info.ExpireTime))
	}

	if len(info.Ratings) != 0 {
		t.Errorf("got %d ratings for an object with none", len(info.Ratings))
	}

	withPassword, nexError := s.GetObjectInfoByDataIDWithPassword(dataID, 0)
	mustSucceed(t, nexError)

	if withPassword.Name != info.Name || withPassword.DataID != info.DataID {
		t.Errorf("info with password differs: %q (%d)", withPassword.Name, withPassword.DataID)
	}

	owner, nexError := s.GetObjectOwnerByDataID(dataID)
	mustSucceed(t, nexError)

	size, nexError := s.GetObjectSizeByDataID(dataID)
	mustSucceed(t, nexError)

	if owner != 1000 || size != 4321 {
		t.Errorf("got owner %d and size %d", owner, size)
	}

	_, nexError = s.GetObjectOwnerByDataID(12345)
	mustFail(t, nexError, nex.ResultCodes.DataStore.NotFound, "owner of missing object")

	_, nexError = s.GetObjectSizeByDataID(12345)
	mustFail(t, nexError, nex.ResultCodes.DataStore.NotFound, "size of missing object")
}

func testAttachFile(t *testing.T, s store.Store, _ Backend) {
	course := createCourse(t, s, 1000, "course")

	param := datastore_smm_types.NewDataStoreAttachFileParam()
	param.PostParam.Size = 2048
	param.PostParam.Name = "preview"
	param.PostParam.DataType = 2
	param.ReferDataID = course

	dataID, nexError := s.InitializeObjectByAttachFileParam(1000, param)
	mustSucceed(t, nexError)

	if dataID != course+1 {
		t.Errorf("attached file got DataID %d, want %d", dataID, course+1)
	}

	mustSucceed(t, s.UpdateObjectUploadCompletedByDataID(dataID, true))

	info, nexError := s.GetObjectInfoByDataID(dataID)
	mustSucceed(t, nexError)

	if uint64(info.ReferDataID) != uint64(course) || info.Size != 2048 || info.OwnerID != 1000 || info.DataType != 2 {
		t.Errorf("got refer DataID %d, size %d, owner %d, data type %d", info.ReferDataID, info.Size, info.OwnerID, info.DataType)
	}
}

func testObjectUpdates(t *testing.T, s store.Store, _ Backend) {
	dataID := createObject(t, s, objectOptions{owner: 1000, name: "object", dataType: courseDataType})

	mustSucceed(t, s.UpdateObjectPeriodByDataIDWithPassword(dataID, 30, 0))
	mustSucceed(t, s.UpdateObjectMetaBinaryByDataIDWithPassword(dataID, types.QBuffer{9, 8, 7}, 0))
	mustSucceed(t, s.UpdateObjectDataTypeByDataIDWithPassword(dataID, 7, 0))

	info, nexError := s.GetObjectInfoByDataID(dataID)
	mustSucceed(t, nexError)

	if info.Period != 30 || info.DataType != 7 || !bytes.Equal(info.MetaBinary, []byte{9, 8, 7}) {
		t.Errorf("got period %d, data type %d, meta binary %v", info.Period, info.DataType, info.MetaBinary)
	}

	mustFail(t, s.UpdateObjectPeriodByDataIDWithPassword(12345, 30, 0), nex.ResultCodes.DataStore.NotFound, "period of missing object")
	mustFail(t, s.UpdateObjectMetaBinaryByDataIDWithPassword(12345, types.QBuffer{1}, 0), nex.ResultCodes.DataStore.NotFound, "meta binary of missing object")
	mustFail(t, s.UpdateObjectDataTypeByDataIDWithPassword(12345, 7, 0), nex.ResultCodes.DataStore.NotFound, "data type of missing object")

	mustSucceed(t, s.DeleteObjectByDataID(dataID))
	mustFail(t, s.UpdateObjectPeriodByDataIDWithPassword(dataID, 60, 0), nex.ResultCodes.DataStore.NotFound, "period of deleted object")
}

func testPersistenceTarget(t *testing.T, s store.Store, _ Backend) {
	dataID := createObject(t, s, objectOptions{owner: 1000, name: "maker", dataType: 1, persistenceSlotID: 3, ratingSlots: 1})
	createObject(t, s, objectOptions{owner: 1000, name: "other slot", dataType: 1, persistenceSlotID: 4})
	createObject(t, s, objectOptions{owner: 2000, name: "other owner", dataType: 1, persistenceSlotID: 3})

	target := datastore_types.NewDataStorePersistenceTarget()
	target.OwnerID = 1000
	target.PersistenceSlotID = 3

	info, nexError := s.GetObjectInfoByPersistenceTargetWithPassword(target, 0)
	mustSucceed(t, nexError)

	if info.DataID != dataID || info.Name != "maker" || len(info.Ratings) != 1 {
		t.Errorf("got object %d (%q) with %d ratings, want %d (\"maker\") with 1", info.DataID, info.Name, len(info.Ratings), dataID)
	}

	target.OwnerID = 3000

	_, nexError = s.GetObjectInfoByPersistenceTargetWithPassword(target, 0)
	mustFail(t, nexError, nex.ResultCodes.DataStore.NotFound, "persistence target of an owner with no objects")

	target.OwnerID = 1000
	mustSucceed(t, s.DeleteObjectByDataID(dataID))

	_, nexError = s.GetObjectInfoByPersistenceTargetWithPassword(target, 0)
	mustFail(t, nexError, nex.ResultCodes.DataStore.NotFound, "persistence target of a deleted object")
}

func ratingsBySlot(ratings []datastore_types.DataStoreRatingInfoWithSlot) map[int]datastore_types.DataStoreRatingInfo {
	bySlot := make(map[int]datastore_types.DataStoreRatingInfo, len(ratings))
	for _, rating := range ratings {
		bySlot[int(rating.Slot)] = rating.Rating
	}

	return bySlot
}

func testRatings(t *testing.T, s store.Store, _ Backend) {
	created, nexError := s.InitializeObjectByPreparePostParam(1000, datastore_types.NewDataStorePreparePostParam())
	mustSucceed(t, nexError)

	dataID := types.UInt64(created)

	for slot, initialValue := range []int64{0, 100} {
		rating := datastore_types.NewDataStoreRatingInitParamWithSlot()
		rating.Slot = types.Int8(slot)
		rating.Param.InitialValue = types.Int64(initialValue)

		mustSucceed(t, s.InitializeObjectRatingWithSlot(created, rating))
	}

	_, nexError = s.GetObjectRatingsWithSlotByDataID(dataID)
	mustFail(t, nexError, nex.ResultCodes.DataStore.NotFound, "ratings of object before its upload completes")

	_, nexError = s.RateObjectWithPassword(dataID, 0, 1, 0)
	mustFail(t, nexError, nex.ResultCodes.DataStore.NotFound, "rating an object before its upload completes")

	mustSucceed(t, s.UpdateObjectUploadCompletedByDataID(dataID, true))

	ratings, nexError := s.GetObjectRatingsWithSlotByDataID(dataID)
	mustSucceed(t, nexError)

	bySlot := ratingsBySlot(ratings)
	if len(bySlot) != 2 || bySlot[0].TotalValue != 0 || bySlot[0].Count != 0 || bySlot[1].TotalValue != 100 || bySlot[1].InitialValue != 100 {
		t.Fatalf("got new ratings %+v", bySlot)
	}

	rated, nexError := s.RateObjectWithPassword(dataID, 0, 5, 0)
	mustSucceed(t, nexError)

	if rated.TotalValue != 5 || rated.Count != 1 {
		t.Errorf("first rating gave total %d and count %d, want 5 and 1", rated.TotalValue, rated.Count)
	}

	rated, nexError = s.RateObjectWithPassword(dataID, 0, 7, 0)
	mustSucceed(t, nexError)

	if rated.TotalValue != 12 || rated.Count != 2 {
		t.Errorf("second rating gave total %d and count %d, want 12 and 2", rated.TotalValue, rated.Count)
	}

	rated, nexError = s.RateObjectWithPassword(dataID, 1, 1, 0)
	mustSucceed(t, nexError)

	if rated.TotalValue != 101 || rated.Count != 1 || rated.InitialValue != 100 {
		t.Errorf("rating slot 1 gave total %d, count %d, initial value %d, want 101, 1, 100", rated.TotalValue, rated.Count, rated.InitialValue)
	}

	_, nexError = s.RateObjectWithPassword(dataID, 9, 1, 0)
	mustFail(t, nexError, nex.ResultCodes.DataStore.InvalidArgument, "rating a slot which does not exist")

	info, nexError := s.GetObjectInfoByDataID(dataID)
	mustSucceed(t, nexError)

	bySlot = ratingsBySlot(info.Ratings)
	if len(bySlot) != 2 || bySlot[0].TotalValue != 12 || bySlot[0].Count != 2 || bySlot[1].TotalValue != 101 {
		t.Errorf("object info has ratings %+v", bySlot)
	}

	withPassword, nexError := s.GetObjectRatingsWithSlotByDataIDWithPassword(dataID, 0)
	mustSucceed(t, nexError)

	if len(withPassword) != 2 {
		t.Errorf("got %d ratings with password, want 2", len(withPassword))
	}
}

func testCustomRankings(t *testing.T, s store.Store, _ Backend) {
	first := createCourse(t, s, 1000, "first")
	second := createCourse(t, s, 1000, "second")

	mustSucceed(t, s.InsertOrUpdateCustomRanking(first, 0, 5))
	mustSucceed(t, s.InsertOrUpdateCustomRanking(first, 0, 3))
	mustSucceed(t, s.InsertOrUpdateCustomRanking(first, 400000000, 50))
	mustSucceed(t, s.InsertOrUpdateCustomRanking(second, 0, 1))

	mustFail(t, s.InsertOrUpdateCustomRanking(12345, 0, 1), nex.ResultCodes.DataStore.NotFound, "ranking a missing object")

	// * Missing objects are left out, duplicates are kept, and the order is the one asked for
	results := s.GetCustomRankingsByDataIDs(0, types.List[types.UInt64]{second, 12345, first, second})
	if len(results) != 3 {
		t.Fatalf("got %d rankings, want 3", len(results))
	}

	want := []struct {
		dataID types.UInt64
		score  types.UInt32
		name   types.String
	}{{second, 1, "second"}, {first, 8, "first"}, {second, 1, "second"}}

	for i := range want {
		if results[i].MetaInfo.DataID != want[i].dataID || results[i].Score != want[i].score || results[i].MetaInfo.Name != want[i].name || results[i].Order != 0 {
			t.Errorf("ranking %d is object %d (%q) with score %d and order %d, want %d (%q) with score %d and order 0", i, results[i].MetaInfo.DataID, results[i].MetaInfo.Name, results[i].Score, results[i].Order, want[i].dataID, want[i].name, want[i].score)
		}
	}

	if len(results[0].MetaInfo.Ratings) != 2 {
		t.Errorf("ranking has %d ratings, want 2", len(results[0].MetaInfo.Ratings))
	}

	other := s.GetCustomRankingsByDataIDs(400000000, types.List[types.UInt64]{first, second})
	if len(other) != 1 || other[0].MetaInfo.DataID != first || other[0].Score != 50 {
		t.Errorf("got %d rankings for another application ID, want only the first course with score 50", len(other))
	}

	mustSucceed(t, s.DeleteObjectByDataID(first))

	if remaining := s.GetCustomRankingsByDataIDs(0, types.List[types.UInt64]{first, second}); len(remaining) != 1 || remaining[0].MetaInfo.DataID != second {
		t.Errorf("got %d rankings after deleting a course, want only the second course", len(remaining))
	}
}

func testCourseRecords(t *testing.T, s store.Store, _ Backend) {
	course := createCourse(t, s, 1000, "course")

	_, nexError := s.GetCourseRecordByDataIDAndSlot(course, 0)
	mustFail(t, nexError, nex.ResultCodes.DataStore.NotFound, "record of a course nobody has cleared")

	mustSucceed(t, s.InsertOrUpdateCourseRecord(course, 0, 2001, 5000))

	record, nexError := s.GetCourseRecordByDataIDAndSlot(course, 0)
	mustSucceed(t, nexError)

	if record.DataID != course || record.Slot != 0 || record.FirstPID != 2001 || record.BestPID != 2001 || record.BestScore != 5000 || record.CreatedTime == 0 || record.UpdatedTime == 0 {
		t.Errorf("first clear gave %+v", record)
	}

	// * A slower clear changes nothing
	mustSucceed(t, s.InsertOrUpdateCourseRecord(course, 0, 2002, 6000))

	record, nexError = s.GetCourseRecordByDataIDAndSlot(course, 0)
	mustSucceed(t, nexError)

	if record.FirstPID != 2001 || record.BestPID != 2001 || record.BestScore != 5000 {
		t.Errorf("slower clear gave first %d, best %d, score %d, want 2001, 2001, 5000", record.FirstPID, record.BestPID, record.BestScore)
	}

	// * A faster clear takes the record, but not the first clear
	mustSucceed(t, s.InsertOrUpdateCourseRecord(course, 0, 2003, 4000))

	record, nexError = s.GetCourseRecordByDataIDAndSlot(course, 0)
	mustSucceed(t, nexError)

	if record.FirstPID != 2001 || record.BestPID != 2003 || record.BestScore != 4000 {
		t.Errorf("faster clear gave first %d, best %d, score %d, want 2001, 2003, 4000", record.FirstPID, record.BestPID, record.BestScore)
	}

	_, nexError = s.GetCourseRecordByDataIDAndSlot(course, 1)
	mustFail(t, nexError, nex.ResultCodes.DataStore.NotFound, "record in another slot")

	mustFail(t, s.InsertOrUpdateCourseRecord(12345, 0, 2001, 1), nex.ResultCodes.DataStore.NotFound, "record on a missing course")
}

func testBufferQueues(t *testing.T, s store.Store, _ Backend) {
	maker := createObject(t, s, objectOptions{owner: 1000, name: "maker", dataType: 1})

	buffers, nexError := s.GetBufferQueuesByDataIDAndSlot(maker, 0)
	mustSucceed(t, nexError)

	if len(buffers) != 0 {
		t.Fatalf("got %d buffers from an empty queue", len(buffers))
	}

	mustSucceed(t, s.InsertOrUpdateBufferQueueData(maker, 0, types.QBuffer{1, 1}))
	mustSucceed(t, s.InsertOrUpdateBufferQueueData(maker, 0, types.QBuffer{2, 2}))
	mustSucceed(t, s.InsertOrUpdateBufferQueueData(maker, 1, types.QBuffer{3, 3}))

	// * The same buffer again is not added a second time
	mustSucceed(t, s.InsertOrUpdateBufferQueueData(maker, 0, types.QBuffer{2, 2}))

	buffers, nexError = s.GetBufferQueuesByDataIDAndSlot(maker, 0)
	mustSucceed(t, nexError)

	if len(buffers) != 2 || !bytes.Equal(buffers[0], []byte{1, 1}) || !bytes.Equal(buffers[1], []byte{2, 2}) {
		t.Errorf("slot 0 has buffers %v, want [[1 1] [2 2]]", buffers)
	}

	buffers, nexError = s.GetBufferQueuesByDataIDAndSlot(maker, 1)
	mustSucceed(t, nexError)

	if len(buffers) != 1 || !bytes.Equal(buffers[0], []byte{3, 3}) {
		t.Errorf("slot 1 has buffers %v, want [[3 3]]", buffers)
	}

	_, nexError = s.GetBufferQueuesByDataIDAndSlot(12345, 0)
	mustFail(t, nexError, nex.ResultCodes.DataStore.NotFound, "buffers of a missing object")

	mustFail(t, s.InsertOrUpdateBufferQueueData(12345, 0, types.QBuffer{1}), nex.ResultCodes.DataStore.NotFound, "adding a buffer to a missing object")
}

func testUserCourseObjectIDs(t *testing.T, s store.Store, _ Backend) {
	want := map[types.UInt64]bool{
		createObject(t, s, objectOptions{owner: 1000, name: "course", dataType: 3}):          true,
		createObject(t, s, objectOptions{owner: 1000, name: "another course", dataType: 49}): true,
	}

	createObject(t, s, objectOptions{owner: 1000, name: "maker", dataType: 1})
	createObject(t, s, objectOptions{owner: 1000, name: "attached file", dataType: 2})
	createObject(t, s, objectOptions{owner: 1000, name: "event metadata", dataType: 50})
	createObject(t, s, objectOptions{owner: 1000, name: "not uploaded", dataType: courseDataType, incomplete: true})
	createObject(t, s, objectOptions{owner: 2000, name: "someone else's", dataType: courseDataType})

	deleted := createObject(t, s, objectOptions{owner: 1000, name: "deleted", dataType: courseDataType})
	mustSucceed(t, s.DeleteObjectByDataID(deleted))

	dataIDs, nexError := s.GetUserCourseObjectIDs(1000)
	mustSucceed(t, nexError)

	if len(dataIDs) != len(want) {
		t.Fatalf("got courses %v, want %d of them", dataIDs, len(want))
	}

	for _, dataID := range dataIDs {
		if !want[dataID] {
			t.Errorf("got object %d, which is not one of the owner's available courses", dataID)
		}
	}

	none, nexError := s.GetUserCourseObjectIDs(3000)
	mustSucceed(t, nexError)

	if len(none) != 0 {
		t.Errorf("got %d courses for an owner with none", len(none))
	}
}

func testRandomCourses(t *testing.T, s store.Store, _ Backend) {
	empty, nexError := s.GetRandomCoursesWithLimit(50)
	mustSucceed(t, nexError)

	if len(empty) != 0 {
		t.Fatalf("got %d courses from an empty store", len(empty))
	}

	names := map[types.UInt64]string{}

	for i := range 130 {
		name := fmt.Sprintf("course %d", i)
		dataID := createCourse(t, s, uint64(1000+i), name)
		names[dataID] = name

		_, nexError := s.RateObjectWithPassword(dataID, 1, types.Int32(i), 0)
		mustSucceed(t, nexError)
	}

	seen := map[types.UInt64]bool{}

	for _, limit := range []int{1, 32, 50, 100} {
		for range 5 {
			courses, nexError := s.GetRandomCoursesWithLimit(limit)
			mustSucceed(t, nexError)

			if len(courses) != limit {
				t.Fatalf("limit %d: got %d courses", limit, len(courses))
			}

			inResult := map[types.UInt64]bool{}

			for _, course := range courses {
				dataID := course.MetaInfo.DataID

				if inResult[dataID] {
					t.Fatalf("limit %d: course %d returned twice", limit, dataID)
				}

				inResult[dataID] = true
				seen[dataID] = true

				i := int(dataID) - store.FirstDataID

				if string(course.MetaInfo.Name) != names[dataID] || int(course.MetaInfo.OwnerID) != 1000+i || course.Order != 0 {
					t.Errorf("course %d has name %q, owner %d, order %d", dataID, course.MetaInfo.Name, course.MetaInfo.OwnerID, course.Order)
				}

				bySlot := ratingsBySlot(course.MetaInfo.Ratings)
				if len(bySlot) != 2 || int(bySlot[1].TotalValue) != i {
					t.Errorf("course %d has ratings %+v, want 2 with slot 1 totalling %d", dataID, bySlot, i)
				}
			}
		}
	}

	// * 915 picks from 130 courses. Seeing under half
	// * of them would mean the picks are not random
	if len(seen) < 65 {
		t.Errorf("only %d of 130 courses were ever returned", len(seen))
	}

	for _, limit := range []int{0, -1} {
		courses, nexError := s.GetRandomCoursesWithLimit(limit)
		mustSucceed(t, nexError)

		if len(courses) != 0 {
			t.Errorf("limit %d: got %d courses, want 0", limit, len(courses))
		}
	}

	courses, nexError := s.GetRandomCoursesWithLimit(store.MaxRandomCourses + 1000)
	mustSucceed(t, nexError)

	if len(courses) != store.MaxRandomCourses {
		t.Errorf("got %d courses, want the maximum of %d", len(courses), store.MaxRandomCourses)
	}
}

func testRandomCoursesSkipUnavailable(t *testing.T, s store.Store, backend Backend) {
	available := map[types.UInt64]bool{}

	// * Unavailable objects are placed all around the available
	// * ones, including at both ends of the DataID range
	for i := range 60 {
		switch i % 5 {
		case 0:
			deleted := createCourse(t, s, 1000, "deleted")
			mustSucceed(t, s.DeleteObjectByDataID(deleted))
		case 1:
			underReview := createCourse(t, s, 1000, "under review")
			backend.SetUnderReview(t, uint64(underReview))
		case 2:
			createObject(t, s, objectOptions{owner: 1000, name: "not uploaded", dataType: courseDataType, incomplete: true})
		case 3:
			// * Has no custom ranking for application ID 0, like a non-course object
			createObject(t, s, objectOptions{owner: 1000, name: "not a course", dataType: 1})
		default:
			available[createCourse(t, s, 1000, "available")] = true
		}
	}

	for _, limit := range []int{5, 12, 100} {
		for range 10 {
			courses, nexError := s.GetRandomCoursesWithLimit(limit)
			mustSucceed(t, nexError)

			if want := min(limit, len(available)); len(courses) != want {
				t.Fatalf("limit %d: got %d courses, want %d", limit, len(courses), want)
			}

			for _, course := range courses {
				if !available[course.MetaInfo.DataID] {
					t.Fatalf("limit %d: unavailable object %d (%q) returned", limit, course.MetaInfo.DataID, course.MetaInfo.Name)
				}
			}
		}
	}
}

func testUnderReview(t *testing.T, s store.Store, backend Backend) {
	course := createCourse(t, s, 1000, "course")

	backend.SetUnderReview(t, uint64(course))

	mustFail(t, s.IsObjectAvailable(course), nex.ResultCodes.DataStore.UnderReviewing, "object under review")
	mustFail(t, s.IsObjectAvailableWithPassword(course, 0), nex.ResultCodes.DataStore.UnderReviewing, "object under review, with password")

	_, nexError := s.GetObjectInfoByDataID(course)
	mustFail(t, nexError, nex.ResultCodes.DataStore.UnderReviewing, "info of object under review")

	mustFail(t, s.UpdateObjectPeriodByDataIDWithPassword(course, 30, 0), nex.ResultCodes.DataStore.UnderReviewing, "period of object under review")
	mustFail(t, s.InsertOrUpdateCourseRecord(course, 0, 2001, 1), nex.ResultCodes.DataStore.UnderReviewing, "record on a course under review")

	dataIDs, nexError := s.GetUserCourseObjectIDs(1000)
	mustSucceed(t, nexError)

	if len(dataIDs) != 0 {
		t.Errorf("got %d courses for an owner whose only course is under review", len(dataIDs))
	}
}

func testEventCourseMetadata(t *testing.T, s store.Store, _ Backend) {
	if err := s.EnsureEventCourseMetadata(111); err != nil {
		t.Fatal(err)
	}

	info, nexError := s.GetObjectInfoByDataID(store.EventCourseMetadataDataID)
	mustSucceed(t, nexError)

	if info.Size != 111 || info.DataType != 50 || info.OwnerID != 2 || info.DelPermission.Permission != 3 || info.Permission.Permission != 0 {
		t.Errorf("got size %d, data type %d, owner %d, delete permission %d, permission %d", info.Size, info.DataType, info.OwnerID, info.DelPermission.Permission, info.Permission.Permission)
	}

	// * Running it again, as every server start does, updates the size
	if err := s.EnsureEventCourseMetadata(222); err != nil {
		t.Fatal(err)
	}

	size, nexError := s.GetObjectSizeByDataID(store.EventCourseMetadataDataID)
	mustSucceed(t, nexError)

	if size != 222 {
		t.Errorf("got size %d after the file changed, want 222", size)
	}

	// * The reserved DataID must not change where new objects start
	if dataID := createObject(t, s, objectOptions{owner: 1000, name: "course", dataType: courseDataType}); dataID != store.FirstDataID {
		t.Errorf("first object got DataID %d, want %d", dataID, store.FirstDataID)
	}
}
