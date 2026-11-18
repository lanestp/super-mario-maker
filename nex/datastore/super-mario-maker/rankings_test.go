package nex_datastore_super_mario_maker

import (
	"testing"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_super_mario_maker "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker"
	datastore_super_mario_maker_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

// * The custom ranking application IDs a real game uses for makers
const (
	makerRankingApplication       = 300000000
	makerRankingWindowApplication = 300002400
)

// * Every ResultOption bit the handlers look at: tags, ratings, meta
// * binary and score
const allResultOptions = 0x1 | 0x2 | 0x4 | 0x20

type rating struct {
	dataID        types.UInt64
	applicationID uint32
	score         uint32
}

// * Rates objects in custom rankings, as a game does when a player gives
// * a course a star or a maker is ranked
func rate(t *testing.T, ratings ...rating) {
	t.Helper()

	params := make(types.List[datastore_super_mario_maker_types.DataStoreRateCustomRankingParam], 0, len(ratings))
	for _, r := range ratings {
		param := datastore_super_mario_maker_types.NewDataStoreRateCustomRankingParam()
		param.DataID = r.dataID
		param.ApplicationID = types.UInt32(r.applicationID)
		param.Score = types.UInt32(r.score)

		params = append(params, param)
	}

	rmc, nexError := RateCustomRanking(nil, from(1000), 5, params)
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodRateCustomRanking, 5))
}

// * Asks for objects in a ranking by DataID, as a game does
func rankingsByDataID(t *testing.T, applicationID uint32, resultOption uint8, dataIDs ...types.UInt64) types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult] {
	t.Helper()

	param := datastore_super_mario_maker_types.NewDataStoreGetCustomRankingByDataIDParam()
	param.ApplicationID = types.UInt32(applicationID)
	param.DataIDList = types.List[types.UInt64](dataIDs)
	param.ResultOption = types.UInt8(resultOption)

	rmc, nexError := GetCustomRankingByDataID(nil, from(1000), 11, param)

	var rankings types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult]
	var results types.List[types.QResult]
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodGetCustomRankingByDataID, 11), &rankings, &results)

	wantAllSucceeded(t, results, len(rankings))

	return rankings
}

func TestStarsGivenToACourseAddUpInItsRanking(t *testing.T) {
	server := setUp(t)

	course := addRankedCourse(t, server.store, object{}, 0)

	rate(t, rating{course, 0, 1})
	rate(t, rating{course, 0, 1}, rating{course, 0, 1})

	rankings := rankingsByDataID(t, 0, allResultOptions, course)
	if len(rankings) != 1 || rankings[0].Score != 3 {
		t.Fatalf("got rankings %v, want the course with 3 stars", dataIDsOf(rankings))
	}
}

func TestMakersAreRankedInTheirOwnApplications(t *testing.T) {
	server := setUp(t)

	maker := addMaker(t, server.store, 1000)
	other := addMaker(t, server.store, 2000)

	rate(t,
		rating{maker, makerRankingApplication, 12},
		rating{maker, makerRankingWindowApplication, 3},
		rating{other, makerRankingApplication, 20},
	)

	rankings := rankingsByDataID(t, makerRankingApplication, allResultOptions, maker, other)
	if len(rankings) != 2 || rankings[0].Score != 12 || rankings[1].Score != 20 {
		t.Fatalf("got scores %v, want 12 and 20 in the order asked for", rankings)
	}

	// * A maker is data type 1 with 9 rating slots
	for _, ranking := range rankings {
		if ranking.MetaInfo.DataType != 1 || len(ranking.MetaInfo.Ratings) != 9 {
			t.Errorf("maker %d has data type %d and %d rating slots, want 1 and 9", ranking.MetaInfo.DataID, ranking.MetaInfo.DataType, len(ranking.MetaInfo.Ratings))
		}
	}

	window := rankingsByDataID(t, makerRankingWindowApplication, allResultOptions, maker, other)
	if len(window) != 1 || window[0].MetaInfo.DataID != maker || window[0].Score != 3 {
		t.Errorf("got %v in the windowed ranking, want only the maker with 3", dataIDsOf(window))
	}

	// * Courses are not in a maker ranking, nor makers in the stars ranking
	if stars := rankingsByDataID(t, 0, allResultOptions, maker); len(stars) != 0 {
		t.Errorf("a maker was found in the stars ranking")
	}
}

func TestRatingAMissingCourseChangesNothingAndStillSucceeds(t *testing.T) {
	server := setUp(t)

	course := addRankedCourse(t, server.store, object{}, 5)
	deleted := addRankedCourse(t, server.store, object{}, 5)
	deleteObject(t, server.store, deleted)

	rate(t, rating{deleted, 0, 1}, rating{course + 100, 0, 1}, rating{course, 0, 1})

	if rankings := rankingsByDataID(t, 0, allResultOptions, course); len(rankings) != 1 || rankings[0].Score != 6 {
		t.Errorf("the course that exists got %v, want 6 stars", rankings)
	}
}

func TestRankingsByDataIDLeaveOutWhatWasNotAskedFor(t *testing.T) {
	server := setUp(t)

	course := addRankedCourse(t, server.store, object{tags: []string{"a", "b"}, metaBinary: []byte{9, 9, 9}}, 7)

	type kept struct{ tags, ratings, metaBinary, score int }

	for option, want := range map[uint8]kept{
		0x00:             {0, 0, 0, 0},
		0x01:             {2, 0, 0, 0},
		0x02:             {0, 5, 0, 0},
		0x04:             {0, 0, 3, 0},
		0x20:             {0, 0, 0, 7},
		allResultOptions: {2, 5, 3, 7},
	} {
		rankings := rankingsByDataID(t, 0, option, course)
		if len(rankings) != 1 {
			t.Fatalf("result option 0x%X gave %d rankings, want 1", option, len(rankings))
		}

		info := rankings[0].MetaInfo
		got := kept{len(info.Tags), len(info.Ratings), len(info.MetaBinary), int(rankings[0].Score)}

		if got != want {
			t.Errorf("result option 0x%X kept %+v, want %+v", option, got, want)
		}

		if info.DataID != course {
			t.Errorf("result option 0x%X gave course %d, want %d", option, info.DataID, course)
		}
	}
}

func TestRankingsByDataIDSkipWhatIsMissingAndRepeatWhatIsAskedForTwice(t *testing.T) {
	server := setUp(t)

	first := addRankedCourse(t, server.store, object{}, 1)
	second := addRankedCourse(t, server.store, object{}, 2)
	unranked := addObject(t, server.store, object{})
	deleted := addRankedCourse(t, server.store, object{}, 3)
	deleteObject(t, server.store, deleted)

	t.Setenv("PN_SMM_LOG_LOOKUPS", "1")

	rankings := rankingsByDataID(t, 0, allResultOptions, first, unranked, deleted, second+100, second, first)
	if got := dataIDsOf(rankings); !sameIDs(got, first, second, first) {
		t.Errorf("got %v, want %d, %d and %d again", got, first, second, first)
	}
}

func TestTheRateCounterOfApplicationZeroIsBelowItsThreshold(t *testing.T) {
	setUp(t)

	for applicationID, want := range map[uint32]bool{0: true, 1: false, makerRankingApplication: false} {
		rmc, nexError := CheckRateCustomRankingCounter(nil, from(1000), 3, types.UInt32(applicationID))

		var below types.Bool
		read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodCheckRateCustomRankingCounter, 3), &below)

		if bool(below) != want {
			t.Errorf("application %d is below its threshold: %v, want %v", applicationID, below, want)
		}
	}
}

func rankingParam(applicationID uint32, offset, length uint32, resultOption uint8) datastore_super_mario_maker_types.DataStoreGetCustomRankingParam {
	param := datastore_super_mario_maker_types.NewDataStoreGetCustomRankingParam()
	param.ApplicationID = types.UInt32(applicationID)
	param.ResultOption = types.UInt8(resultOption)
	param.ResultRange.Offset = types.UInt32(offset)
	param.ResultRange.Length = types.UInt32(length)

	return param
}

func customRanking(t *testing.T, param datastore_super_mario_maker_types.DataStoreGetCustomRankingParam) types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult] {
	t.Helper()

	rmc, nexError := GetCustomRanking(nil, from(1000), 12, param)

	var rankings types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult]
	var results types.List[types.QResult]
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodGetCustomRanking, 12), &rankings, &results)

	wantAllSucceeded(t, results, len(rankings))

	return rankings
}

func TestARankingListsTheMostStarsFirst(t *testing.T) {
	server := setUp(t)

	low := addRankedCourse(t, server.store, object{}, 1)
	high := addRankedCourse(t, server.store, object{}, 50)
	middle := addRankedCourse(t, server.store, object{}, 7)
	tied := addRankedCourse(t, server.store, object{}, 7)
	deleted := addRankedCourse(t, server.store, object{}, 99)
	deleteObject(t, server.store, deleted)
	maker := addMaker(t, server.store, 1000)
	rate(t, rating{maker, makerRankingApplication, 1000})

	rankings := customRanking(t, rankingParam(0, 0, 10, allResultOptions))
	if got := dataIDsOf(rankings); !sameIDs(got, high, middle, tied, low) {
		t.Errorf("got %v, want %v", got, []types.UInt64{high, middle, tied, low})
	}

	if rankings[0].Score != 50 || rankings[3].Score != 1 {
		t.Errorf("got scores %d to %d, want 50 to 1", rankings[0].Score, rankings[3].Score)
	}

	if got := dataIDsOf(customRanking(t, rankingParam(0, 1, 2, allResultOptions))); !sameIDs(got, middle, tied) {
		t.Errorf("second and third gave %v, want %d and %d", got, middle, tied)
	}

	if got := customRanking(t, rankingParam(0, 0, 10, 0)); len(got) != 4 || got[0].Score != 0 || len(got[0].MetaInfo.Ratings) != 0 {
		t.Errorf("without asking for scores or ratings got %d rankings, the first with score %d and %d ratings, want 4 with neither", len(got), got[0].Score, len(got[0].MetaInfo.Ratings))
	}

	if got := dataIDsOf(customRanking(t, rankingParam(makerRankingApplication, 0, 10, allResultOptions))); !sameIDs(got, maker) {
		t.Errorf("the maker ranking gave %v, want only the maker %d", got, maker)
	}
}

func TestARankingLeavesOutWhatWasNotAskedFor(t *testing.T) {
	server := setUp(t)

	addRankedCourse(t, server.store, object{tags: []string{"a"}, metaBinary: []byte{1}}, 4)

	type kept struct{ tags, ratings, metaBinary, score int }

	for option, want := range map[uint8]kept{
		0x00:             {0, 0, 0, 0},
		0x01:             {1, 0, 0, 0},
		0x02:             {0, 5, 0, 0},
		0x04:             {0, 0, 1, 0},
		0x20:             {0, 0, 0, 4},
		allResultOptions: {1, 5, 1, 4},
	} {
		rankings := customRanking(t, rankingParam(0, 0, 10, option))
		if len(rankings) != 1 {
			t.Fatalf("result option 0x%X gave %d rankings, want 1", option, len(rankings))
		}

		info := rankings[0].MetaInfo
		if got := (kept{len(info.Tags), len(info.Ratings), len(info.MetaBinary), int(rankings[0].Score)}); got != want {
			t.Errorf("result option 0x%X kept %+v, want %+v", option, got, want)
		}
	}
}

func TestARankingNeverListsMoreThanOneSearchMayHold(t *testing.T) {
	server := setUp(t)

	for range store.MaxRandomCourses + 5 {
		addRankedCourse(t, server.store, object{ratingSlots: 1}, 1)
	}

	t.Setenv("PN_SMM_LOG_LOOKUPS", "1")

	// * Asking for none or for too many both give as many as a search can hold
	for _, length := range []uint32{0, store.MaxRandomCourses + 1, 1 << 31} {
		if got := customRanking(t, rankingParam(0, 0, length, 0)); len(got) != store.MaxRandomCourses {
			t.Errorf("asking for %d gave %d, want %d", length, len(got), store.MaxRandomCourses)
		}
	}

	if got := customRanking(t, rankingParam(0, 0, 3, 0)); len(got) != 3 {
		t.Errorf("asking for 3 gave %d", len(got))
	}
}

func TestARankingFailsWhenTheStoreDoes(t *testing.T) {
	setUp(t)
	useBrokenStore(t)

	rmc, nexError := GetCustomRanking(nil, from(1000), 1, rankingParam(0, 0, 10, 0))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.Unknown)
}

func searchParam(offset, length uint32, resultOption uint8) datastore_types.DataStoreSearchParam {
	param := datastore_types.NewDataStoreSearchParam()
	param.ResultRange.Offset = types.UInt32(offset)
	param.ResultRange.Length = types.UInt32(length)
	param.ResultOption = types.UInt8(resultOption)

	return param
}

type courseSearch func(err error, packet nex.PacketInterface, callID uint32, param datastore_types.DataStoreSearchParam, extraData types.List[types.String]) (*nex.RMCMessage, *nex.Error)

// * Runs a course search and reads the courses it sends back
func search(t *testing.T, handler courseSearch, methodID uint32, param datastore_types.DataStoreSearchParam, extraData types.List[types.String]) types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult] {
	t.Helper()

	rmc, nexError := handler(nil, from(1000), 13, param, extraData)

	var courses types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult]
	read(t, response(t, rmc, nexError, methodID, 13), &courses)

	return courses
}

func TestLatestCoursesComeNewestFirst(t *testing.T) {
	server := setUp(t)

	oldest := addRankedCourse(t, server.store, object{}, 10)
	middle := addRankedCourse(t, server.store, object{}, 0)
	newest := addRankedCourse(t, server.store, object{}, 3)
	reviewed := addRankedCourse(t, server.store, object{}, 3)
	putUnderReview(t, server.store, reviewed)

	// * Neither a maker nor an attached image is a course
	maker := addMaker(t, server.store, 1000)
	rate(t, rating{maker, 0, 1})
	image := addObject(t, server.store, object{dataType: 2})
	rate(t, rating{image, 0, 1})

	courses := search(t, LatestCourseSearchObject, datastore_super_mario_maker.MethodLatestCourseSearchObject, searchParam(0, 10, allResultOptions), extra())
	if got := dataIDsOf(courses); !sameIDs(got, newest, middle, oldest) {
		t.Errorf("got %v, want %v", got, []types.UInt64{newest, middle, oldest})
	}

	if courses[2].Score != 10 {
		t.Errorf("the oldest course has score %d, want its 10 stars", courses[2].Score)
	}

	if got := dataIDsOf(search(t, LatestCourseSearchObject, datastore_super_mario_maker.MethodLatestCourseSearchObject, searchParam(1, 1, 0), extra())); !sameIDs(got, middle) {
		t.Errorf("the second newest gave %v, want %d", got, middle)
	}
}

func TestBestScoreCoursesComeMostStarsFirst(t *testing.T) {
	server := setUp(t)

	few := addRankedCourse(t, server.store, object{}, 2)
	many := addRankedCourse(t, server.store, object{}, 40)
	some := addRankedCourse(t, server.store, object{}, 9)

	t.Setenv("PN_SMM_LOG_LOOKUPS", "1")

	courses := search(t, BestScoreRateCourseSearchObject, datastore_super_mario_maker.MethodBestScoreRateCourseSearchObject, searchParam(0, 0, 0x20), extra())
	if got := dataIDsOf(courses); !sameIDs(got, many, some, few) {
		t.Errorf("got %v, want %v", got, []types.UInt64{many, some, few})
	}

	if courses[0].Score != 40 || len(courses[0].MetaInfo.Tags) != 0 {
		t.Errorf("got score %d and %d tags, want 40 and the tags left out", courses[0].Score, len(courses[0].MetaInfo.Tags))
	}
}

func TestRankedCourseSearchesFailWhenTheStoreDoes(t *testing.T) {
	setUp(t)
	useBrokenStore(t)

	for name, handler := range map[string]courseSearch{
		"latest":     LatestCourseSearchObject,
		"best score": BestScoreRateCourseSearchObject,
	} {
		rmc, nexError := handler(nil, from(1000), 1, searchParam(0, 10, 0), extra())
		if nexError == nil {
			t.Errorf("%s: got a response, want an error", name)
			continue
		}

		wantError(t, rmc, nexError, nex.ResultCodes.DataStore.Unknown)
	}
}
