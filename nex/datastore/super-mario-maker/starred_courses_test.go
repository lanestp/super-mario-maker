package nex_datastore_super_mario_maker

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_super_mario_maker "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker"
	datastore_super_mario_maker_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
)

func queueParam(dataID types.UInt64, slot uint32) datastore_super_mario_maker_types.BufferQueueParam {
	param := datastore_super_mario_maker_types.NewBufferQueueParam()
	param.DataID = dataID
	param.Slot = types.UInt32(slot)

	return param
}

// * Stars courses for a player, as a game does, one request each
func star(t *testing.T, player types.PID, maker types.UInt64, courses ...types.UInt64) {
	t.Helper()

	for _, course := range courses {
		params := types.List[datastore_super_mario_maker_types.BufferQueueParam]{queueParam(maker, 0)}
		buffers := types.List[types.QBuffer]{dataIDBuffer(course)}

		rmc, nexError := AddToBufferQueues(nil, from(player), 1, params, buffers)

		var results types.List[types.QResult]
		read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodAddToBufferQueues, 1), &results)
		wantAllSucceeded(t, results, 1)

		// * Buffers are kept in the order they were added, which is
		// * only told apart by the time they were added
		time.Sleep(2 * time.Millisecond)
	}
}

// * Reads a buffer queue through GetBufferQueue, as a game does
func bufferQueue(t *testing.T, player types.PID, dataID types.UInt64, slot uint32) types.List[types.QBuffer] {
	t.Helper()

	rmc, nexError := GetBufferQueue(nil, from(player), 4, queueParam(dataID, slot))

	var buffers types.List[types.QBuffer]
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodGetBufferQueue, 4), &buffers)

	return buffers
}

// * Reads starred courses from slot 0 of a maker object, where each is
// * an 8 byte little endian DataID
func starredCourses(t *testing.T, player types.PID, maker types.UInt64) []types.UInt64 {
	t.Helper()

	buffers := bufferQueue(t, player, maker, 0)
	courses := make([]types.UInt64, 0, len(buffers))

	for i, buffer := range buffers {
		if len(buffer) != 8 {
			t.Fatalf("starred course %d is %d bytes, want an 8 byte DataID", i, len(buffer))
		}

		courses = append(courses, types.UInt64(binary.LittleEndian.Uint64(buffer)))
	}

	return courses
}

func TestStarringCoursesKeepsThemInTheMakersStarredCourses(t *testing.T) {
	server := setUp(t)

	maker := addMaker(t, server.store, 1000)
	first := addObject(t, server.store, object{owner: 2000})
	second := addObject(t, server.store, object{owner: 3000})

	star(t, 1000, maker, first, second)

	// * Anyone may look at a player's starred courses
	if got := starredCourses(t, 4000, maker); !sameIDs(got, first, second) {
		t.Errorf("got starred courses %v, want %d then %d", got, first, second)
	}
}

func TestStarringACourseAgainMovesItToTheEndWithoutRepeatingIt(t *testing.T) {
	server := setUp(t)

	maker := addMaker(t, server.store, 1000)
	first := addObject(t, server.store, object{})
	second := addObject(t, server.store, object{})

	star(t, 1000, maker, first, second, first)

	if got := starredCourses(t, 1000, maker); !sameIDs(got, second, first) {
		t.Errorf("got starred courses %v, want %d then %d, each once", got, second, first)
	}
}

func TestNobodyCanStarCoursesForAnotherPlayer(t *testing.T) {
	server := setUp(t)

	maker := addMaker(t, server.store, 1000)
	course := addObject(t, server.store, object{})

	params := types.List[datastore_super_mario_maker_types.BufferQueueParam]{queueParam(maker, 0)}
	buffers := types.List[types.QBuffer]{dataIDBuffer(course)}

	rmc, nexError := AddToBufferQueues(nil, from(2000), 1, params, buffers)
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.PermissionDenied)

	if got := starredCourses(t, 1000, maker); len(got) != 0 {
		t.Errorf("got starred courses %v after another player tried, want none", got)
	}
}

func TestBufferQueuesStoreOnlyAsManyBuffersAsThereAreParams(t *testing.T) {
	server := setUp(t)

	maker := addMaker(t, server.store, 1000)
	course := addObject(t, server.store, object{})

	// * Two params and one buffer, and then one param and two buffers:
	// * the real server goes as far as the shorter list
	for _, try := range []struct {
		params  types.List[datastore_super_mario_maker_types.BufferQueueParam]
		buffers types.List[types.QBuffer]
	}{
		{
			types.List[datastore_super_mario_maker_types.BufferQueueParam]{queueParam(maker, 0), queueParam(maker, 1)},
			types.List[types.QBuffer]{dataIDBuffer(course)},
		},
		{
			types.List[datastore_super_mario_maker_types.BufferQueueParam]{queueParam(maker, 2)},
			types.List[types.QBuffer]{types.QBuffer("first"), types.QBuffer("second")},
		},
	} {
		rmc, nexError := AddToBufferQueues(nil, from(1000), 1, try.params, try.buffers)

		var results types.List[types.QResult]
		read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodAddToBufferQueues, 1), &results)
		wantAllSucceeded(t, results, 1)
	}

	if got := starredCourses(t, 1000, maker); !sameIDs(got, course) {
		t.Errorf("slot 0 holds %v, want only %d", got, course)
	}

	if got := bufferQueue(t, 1000, maker, 1); len(got) != 0 {
		t.Errorf("slot 1 holds %d buffers, want none as no buffer went with its param", len(got))
	}

	if got := bufferQueue(t, 1000, maker, 2); len(got) != 1 || string(got[0]) != "first" {
		t.Errorf("slot 2 holds %q, want only the first buffer", got)
	}
}

func TestBufferQueuesOfMissingObjectsAreNotFound(t *testing.T) {
	server := setUp(t)

	maker := addMaker(t, server.store, 1000)
	deleted := addMaker(t, server.store, 1000)
	deleteObject(t, server.store, deleted)

	for _, slot := range []uint32{0, 1} {
		params := types.List[datastore_super_mario_maker_types.BufferQueueParam]{queueParam(deleted, slot)}
		buffers := types.List[types.QBuffer]{dataIDBuffer(maker)}

		rmc, nexError := AddToBufferQueues(nil, from(1000), 1, params, buffers)
		wantError(t, rmc, nexError, nex.ResultCodes.DataStore.NotFound)
	}

	rmc, nexError := GetBufferQueue(nil, from(1000), 1, queueParam(deleted, 0))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.NotFound)
}

func TestAPlayerWithNoStarredCoursesGetsAnEmptyQueue(t *testing.T) {
	server := setUp(t)

	maker := addMaker(t, server.store, 1000)

	if got := bufferQueue(t, 1000, maker, 0); len(got) != 0 {
		t.Errorf("got %d starred courses, want none", len(got))
	}
}

func TestAddingToBufferQueuesWithNothingToAddSendsNoResults(t *testing.T) {
	setUp(t)

	rmc, nexError := AddToBufferQueues(nil, from(1000), 1, nil, nil)

	var results types.List[types.QResult]
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodAddToBufferQueues, 1), &results)
	wantAllSucceeded(t, results, 0)
}
