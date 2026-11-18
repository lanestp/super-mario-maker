package nex_datastore_super_mario_maker

import (
	"errors"
	"slices"
	"testing"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_super_mario_maker "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker"
	datastore_super_mario_maker_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
)

func applicationConfig(t *testing.T, applicationID uint32) []int32 {
	t.Helper()

	rmc, nexError := GetApplicationConfig(nil, from(1000), 14, types.UInt32(applicationID))

	var config types.List[types.Int32]
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodGetApplicationConfig, 14), &config)

	values := make([]int32, 0, len(config))
	for _, value := range config {
		values = append(values, int32(value))
	}

	return values
}

func TestTheWiiUGameplayConfigHoldsTheDifficultyRanges(t *testing.T) {
	setUp(t)

	config := applicationConfig(t, 0)

	if len(config) != 40 {
		t.Fatalf("got %d values, want 40", len(config))
	}

	// * The positions a Wii U game reads them from. Course World goes 0
	// * to 34, 35 to 74, 75 to 95 and 96 to 100, and 100 Mario starts
	// * normal at 0 and asks for 50 courses
	for position, want := range map[int]int32{
		20: 35,  // * Course World normal
		21: 75,  // * Course World expert
		22: 35,  // * 100 Mario easy ends below this
		23: 75,  // * 100 Mario expert
		24: 50,  // * Courses 100 Mario asks for
		25: 0,   // * No second search for new courses
		27: 3,   // * Days in a ranking's time frame
		28: 100, // * Courses Course World downloads
		30: 1,   // * Dothub enabled
		31: 96,  // * 100 Mario super expert
		32: 5,   // * Pick up courses in 100 Mario
		33: 96,  // * Course World super expert
		34: 0,   // * No course upload notice
	} {
		if config[position] != want {
			t.Errorf("value %d is %d, want %d", position, config[position], want)
		}
	}

	// * Everyone may upload 100 courses, whatever their medals
	for position := 10; position < 20; position++ {
		if config[position] != 100 {
			t.Errorf("uploads allowed at medal level %d is %d, want 100", position-10, config[position])
		}
	}

	// * The stars needed for each medal only go up
	if !slices.IsSorted(config[:10]) || config[0] != 1 {
		t.Errorf("stars for each medal are %v, want them going up from 1", config[:10])
	}
}

func TestThe3DSGameplayConfigHoldsTheCourseWorldDifficulties(t *testing.T) {
	setUp(t)

	if got, want := applicationConfig(t, 10), []int32{35, 75, 96, 40, 5, 6}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOtherApplicationConfigsAreReplayedFromNintendo(t *testing.T) {
	setUp(t)

	makers := applicationConfig(t, 1)
	if len(makers) != 9 || makers[0] != 2 || makers[1] != 1770179696 {
		t.Errorf("official makers are %v, want 9 starting with 2 and 1770179696", makers)
	}

	if got, want := applicationConfig(t, 2), []int32{2015, 12, 22, 5, 0}; !slices.Equal(got, want) {
		t.Errorf("bookmark date is %v, want %v", got, want)
	}

	if got := applicationConfig(t, 3); len(got) != 0 {
		t.Errorf("an unknown application gave %v, want nothing", got)
	}
}

func TestApplicationConfigStringsAreTheWordsGamesBlock(t *testing.T) {
	setUp(t)

	for applicationID, want := range map[uint32]struct {
		count    int
		contains string
	}{
		128: {66, "star plz"},
		129: {7, "東日本"},
		130: {61, "ください"},
		131: {0, ""},
	} {
		rmc, nexError := GetApplicationConfigString(nil, from(1000), 15, types.UInt32(applicationID))

		var words types.List[types.String]
		read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodGetApplicationConfigString, 15), &words)

		if len(words) != want.count {
			t.Errorf("application %d gave %d words, want %d", applicationID, len(words), want.count)
		}

		if want.contains != "" && !slices.Contains(words, types.String(want.contains)) {
			t.Errorf("application %d does not block %q", applicationID, want.contains)
		}
	}
}

func TestEveryCourseHasDeletionReasonZero(t *testing.T) {
	server := setUp(t)

	course := addObject(t, server.store, object{})
	deleted := addObject(t, server.store, object{})
	deleteObject(t, server.store, deleted)

	for _, dataIDs := range []types.List[types.UInt64]{ids(), ids(course, deleted, deleted+100)} {
		rmc, nexError := GetDeletionReason(nil, from(1000), 16, dataIDs)

		var reasons types.List[types.UInt32]
		read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodGetDeletionReason, 16), &reasons)

		if len(reasons) != len(dataIDs) {
			t.Fatalf("got %d reasons for %d courses", len(reasons), len(dataIDs))
		}

		for i, reason := range reasons {
			if reason != 0 {
				t.Errorf("course %d has reason %d, want 0", dataIDs[i], reason)
			}
		}
	}
}

func TestEveryHandlerRefusesARequestItCouldNotRead(t *testing.T) {
	setUp(t)

	unreadable := errors.New("Failed to read param from parameters")
	packet := from(1000)
	search := datastore_types.NewDataStoreSearchParam()

	for name, call := range map[string]func() (*nex.RMCMessage, *nex.Error){
		"GetObjectInfos": func() (*nex.RMCMessage, *nex.Error) {
			return GetObjectInfos(unreadable, packet, 1, nil)
		},
		"PrepareAttachFile": func() (*nex.RMCMessage, *nex.Error) {
			return PrepareAttachFile(unreadable, packet, 1, datastore_super_mario_maker_types.NewDataStoreAttachFileParam())
		},
		"CompleteAttachFile": func() (*nex.RMCMessage, *nex.Error) {
			return CompleteAttachFile(unreadable, packet, 1, datastore_types.NewDataStoreCompletePostParam())
		},
		"GetBufferQueue": func() (*nex.RMCMessage, *nex.Error) {
			return GetBufferQueue(unreadable, packet, 1, datastore_super_mario_maker_types.NewBufferQueueParam())
		},
		"AddToBufferQueues": func() (*nex.RMCMessage, *nex.Error) {
			return AddToBufferQueues(unreadable, packet, 1, nil, nil)
		},
		"GetCourseRecord": func() (*nex.RMCMessage, *nex.Error) {
			return GetCourseRecord(unreadable, packet, 1, datastore_super_mario_maker_types.NewDataStoreGetCourseRecordParam())
		},
		"UploadCourseRecord": func() (*nex.RMCMessage, *nex.Error) {
			return UploadCourseRecord(unreadable, packet, 1, datastore_super_mario_maker_types.NewDataStoreUploadCourseRecordParam())
		},
		"GetMetasWithCourseRecord": func() (*nex.RMCMessage, *nex.Error) {
			return GetMetasWithCourseRecord(unreadable, packet, 1, nil, datastore_types.NewDataStoreGetMetaParam())
		},
		"RateCustomRanking": func() (*nex.RMCMessage, *nex.Error) {
			return RateCustomRanking(unreadable, packet, 1, nil)
		},
		"CheckRateCustomRankingCounter": func() (*nex.RMCMessage, *nex.Error) {
			return CheckRateCustomRankingCounter(unreadable, packet, 1, 0)
		},
		"GetCustomRankingByDataID": func() (*nex.RMCMessage, *nex.Error) {
			return GetCustomRankingByDataID(unreadable, packet, 1, datastore_super_mario_maker_types.NewDataStoreGetCustomRankingByDataIDParam())
		},
		"GetCustomRanking": func() (*nex.RMCMessage, *nex.Error) {
			return GetCustomRanking(unreadable, packet, 1, datastore_super_mario_maker_types.NewDataStoreGetCustomRankingParam())
		},
		"LatestCourseSearchObject": func() (*nex.RMCMessage, *nex.Error) {
			return LatestCourseSearchObject(unreadable, packet, 1, search, nil)
		},
		"BestScoreRateCourseSearchObject": func() (*nex.RMCMessage, *nex.Error) {
			return BestScoreRateCourseSearchObject(unreadable, packet, 1, search, nil)
		},
		"FollowingsLatestCourseSearchObject": func() (*nex.RMCMessage, *nex.Error) {
			return FollowingsLatestCourseSearchObject(unreadable, packet, 1, search, nil)
		},
		"SuggestedCourseSearchObject": func() (*nex.RMCMessage, *nex.Error) {
			return SuggestedCourseSearchObject(unreadable, packet, 1, search, nil)
		},
		"CTRPickUpCourseSearchObject": func() (*nex.RMCMessage, *nex.Error) {
			return CTRPickUpCourseSearchObject(unreadable, packet, 1, search, nil)
		},
		"RecommendedCourseSearchObject": func() (*nex.RMCMessage, *nex.Error) {
			return RecommendedCourseSearchObject(unreadable, packet, 1, search, nil)
		},
		"GetDeletionReason": func() (*nex.RMCMessage, *nex.Error) {
			return GetDeletionReason(unreadable, packet, 1, nil)
		},
		"GetApplicationConfig": func() (*nex.RMCMessage, *nex.Error) {
			return GetApplicationConfig(unreadable, packet, 1, 0)
		},
		"GetApplicationConfigString": func() (*nex.RMCMessage, *nex.Error) {
			return GetApplicationConfigString(unreadable, packet, 1, 128)
		},
	} {
		t.Run(name, func(t *testing.T) {
			rmc, nexError := call()
			wantError(t, rmc, nexError, nex.ResultCodes.DataStore.Unknown)

			if nexError.Message != unreadable.Error() {
				t.Errorf("got message %q, want the reading error %q", nexError.Message, unreadable.Error())
			}
		})
	}
}
