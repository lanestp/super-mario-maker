package nex_datastore_super_mario_maker

import (
	"context"
	"strconv"
	"testing"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_super_mario_maker "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

// * The extra data a real game sends for each difficulty
var (
	oneHundredMarioEasy   = extra("1", "0", "34", "", "0")
	oneHundredMarioNormal = extra("1", "0", "74", "", "0")
	courseWorldAll        = extra("", "", "", "0", "0")
	courseWorldEasy       = extra("1", "0", "34", "0", "0")
	courseWorldNormal     = extra("1", "35", "74", "0", "0")
	courseWorldExpert     = extra("1", "75", "95", "0", "0")
	courseWorldSuper      = extra("1", "96", "100", "0", "0")
)

// * Makes a course tried 100 times that fails the given percent of them
func addCourseFailing(t *testing.T, s store.Store, percent int32) types.UInt64 {
	t.Helper()

	return addCourse(t, s, 100, 100-percent)
}

func followingsParam(resultOption uint8, owners ...types.PID) datastore_types.DataStoreSearchParam {
	param := searchParam(0, 100, resultOption)
	param.OwnerIDs = types.List[types.PID](owners)

	return param
}

func TestFollowingsLatestCoursesAreTheCoursesOfThePlayersFollowed(t *testing.T) {
	server := setUp(t)

	first := addRankedCourse(t, server.store, object{owner: 1000}, 4)
	second := addRankedCourse(t, server.store, object{owner: 1000}, 0)
	theirs := addRankedCourse(t, server.store, object{owner: 2000}, 9)
	addRankedCourse(t, server.store, object{owner: 3000}, 1)

	// * Not courses, or not to be shown
	addMaker(t, server.store, 1000)
	addObject(t, server.store, object{owner: 1000, dataType: 2})
	deleted := addRankedCourse(t, server.store, object{owner: 1000}, 1)
	deleteObject(t, server.store, deleted)
	addObject(t, server.store, object{owner: 1000, notUploaded: true})

	t.Setenv("PN_SMM_LOG_LOOKUPS", "1")

	courses := search(t, FollowingsLatestCourseSearchObject, datastore_super_mario_maker.MethodFollowingsLatestCourseSearchObject, followingsParam(allResultOptions, 1000, 2000), extra())
	if got := dataIDsOf(courses); !sameIDs(got, first, second, theirs) {
		t.Fatalf("got %v, want %v", got, []types.UInt64{first, second, theirs})
	}

	if courses[0].Score != 4 || courses[2].Score != 9 || len(courses[0].MetaInfo.Ratings) != 5 {
		t.Errorf("got scores %d and %d with %d ratings, want 4 and 9 with 5", courses[0].Score, courses[2].Score, len(courses[0].MetaInfo.Ratings))
	}
}

func TestFollowingsLatestCoursesLeaveOutWhatWasNotAskedFor(t *testing.T) {
	server := setUp(t)

	addRankedCourse(t, server.store, object{owner: 1000, tags: []string{"a"}, metaBinary: []byte{1}}, 4)

	type kept struct{ tags, ratings, metaBinary, score int }

	for option, want := range map[uint8]kept{
		0x00:             {0, 0, 0, 0},
		0x01:             {1, 0, 0, 0},
		0x02:             {0, 5, 0, 0},
		0x04:             {0, 0, 1, 0},
		0x20:             {0, 0, 0, 4},
		allResultOptions: {1, 5, 1, 4},
	} {
		courses := search(t, FollowingsLatestCourseSearchObject, datastore_super_mario_maker.MethodFollowingsLatestCourseSearchObject, followingsParam(option, 1000), extra())
		if len(courses) != 1 {
			t.Fatalf("result option 0x%X gave %d courses, want 1", option, len(courses))
		}

		info := courses[0].MetaInfo
		if got := (kept{len(info.Tags), len(info.Ratings), len(info.MetaBinary), int(courses[0].Score)}); got != want {
			t.Errorf("result option 0x%X kept %+v, want %+v", option, got, want)
		}
	}
}

func TestFollowingNobodyGivesNoCourses(t *testing.T) {
	server := setUp(t)

	addRankedCourse(t, server.store, object{owner: 1000}, 4)

	if courses := search(t, FollowingsLatestCourseSearchObject, datastore_super_mario_maker.MethodFollowingsLatestCourseSearchObject, followingsParam(0), extra()); len(courses) != 0 {
		t.Errorf("got %d courses, want none", len(courses))
	}
}

func TestFollowingsLatestCoursesFailWhenTheStoreDoes(t *testing.T) {
	setUp(t)
	useBrokenStore(t)

	rmc, nexError := FollowingsLatestCourseSearchObject(nil, from(1000), 1, followingsParam(0, 1000), extra())
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.Unknown)
}

func TestSuggestedCoursesAreAsManyCoursesAsAskedFor(t *testing.T) {
	server := setUp(t)

	current := addRankedCourse(t, server.store, object{}, 0)
	for range 9 {
		addRankedCourse(t, server.store, object{}, 0)
	}

	// * What a real game sends after a course is cleared
	extraData := extra(strconv.FormatUint(uint64(current), 10), "2", "6", "0", "0")

	if courses := search(t, SuggestedCourseSearchObject, datastore_super_mario_maker.MethodSuggestedCourseSearchObject, searchParam(0, 6, 0), extraData); len(courses) != 6 {
		t.Errorf("got %d courses, want 6", len(courses))
	}

	if courses := search(t, SuggestedCourseSearchObject, datastore_super_mario_maker.MethodSuggestedCourseSearchObject, searchParam(0, 50, 0), extraData); len(courses) != 10 {
		t.Errorf("got %d courses when asking for more than there are, want all 10", len(courses))
	}
}

func TestSuggestedCoursesNeedTheCurrentCourseAsANumber(t *testing.T) {
	setUp(t)

	rmc, nexError := SuggestedCourseSearchObject(nil, from(1000), 1, searchParam(0, 6, 0), extra("castle", "2", "6", "0", "0"))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.InvalidArgument)
}

func TestSuggestedCoursesWithNoExtraDataAreRefusedWithoutCrashing(t *testing.T) {

	setUp(t)

	var rmc *nex.RMCMessage
	var nexError *nex.Error

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("the handler panicked: %v", recovered)
			}
		}()

		rmc, nexError = SuggestedCourseSearchObject(nil, from(1000), 1, searchParam(0, 6, 0), extra())
	}()

	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.InvalidArgument)
}

func TestSuggestedCoursesFailWhenTheStoreDoes(t *testing.T) {
	setUp(t)
	useBrokenStore(t)

	rmc, nexError := SuggestedCourseSearchObject(nil, from(1000), 1, searchParam(0, 6, 0), extra("940000"))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.Unknown)
}

func TestThe3DSPickUpGivesAsManyCoursesAsAskedFor(t *testing.T) {
	server := setUp(t)

	for range 12 {
		addRankedCourse(t, server.store, object{}, 0)
	}

	addMaker(t, server.store, 1000)

	if courses := search(t, CTRPickUpCourseSearchObject, datastore_super_mario_maker.MethodCTRPickUpCourseSearchObject, searchParam(0, 10, 0), extra()); len(courses) != 10 {
		t.Errorf("got %d courses, want 10", len(courses))
	}

	if courses := search(t, CTRPickUpCourseSearchObject, datastore_super_mario_maker.MethodCTRPickUpCourseSearchObject, searchParam(0, 40, 0), extra()); len(courses) != 12 {
		t.Errorf("got %d courses when asking for more than there are, want all 12", len(courses))
	}
}

func TestThe3DSPickUpFailsWhenTheStoreDoes(t *testing.T) {
	setUp(t)
	useBrokenStore(t)

	rmc, nexError := CTRPickUpCourseSearchObject(nil, from(1000), 1, searchParam(0, 10, 0), extra())
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.Unknown)
}

// * Runs a recommended course search, as 100 Mario and Course World do
func recommended(t *testing.T, length uint32, extraData types.List[types.String]) []types.UInt64 {
	t.Helper()

	courses := search(t, RecommendedCourseSearchObject, datastore_super_mario_maker.MethodRecommendedCourseSearchObject, searchParam(0, length, 0), extraData)

	seen := map[types.UInt64]bool{}
	for _, id := range dataIDsOf(courses) {
		if seen[id] {
			t.Errorf("course %d was sent twice", id)
		}

		seen[id] = true
	}

	return dataIDsOf(courses)
}

func set(ids ...types.UInt64) map[types.UInt64]bool {
	all := make(map[types.UInt64]bool, len(ids))
	for _, id := range ids {
		all[id] = true
	}

	return all
}

func TestEachDifficultyGetsCoursesOfItsFailureRate(t *testing.T) {
	server := setUp(t)

	easy := set(addCourseFailing(t, server.store, 0), addCourseFailing(t, server.store, 34))
	normal := set(addCourseFailing(t, server.store, 35), addCourseFailing(t, server.store, 74))
	expert := set(addCourseFailing(t, server.store, 75), addCourseFailing(t, server.store, 95))
	superExpert := set(addCourseFailing(t, server.store, 96), addCourseFailing(t, server.store, 100))

	for name, try := range map[string]struct {
		extraData types.List[types.String]
		want      map[types.UInt64]bool
	}{
		"Course World easy":         {courseWorldEasy, easy},
		"Course World normal":       {courseWorldNormal, normal},
		"Course World expert":       {courseWorldExpert, expert},
		"Course World super expert": {courseWorldSuper, superExpert},
	} {
		got := recommended(t, 2, try.extraData)
		if len(got) != 2 || !try.want[got[0]] || !try.want[got[1]] {
			t.Errorf("%s gave %v, want the two courses of its failure rate", name, got)
		}
	}

	// * 100 Mario normal takes anything from 0 to 74
	got := recommended(t, 4, oneHundredMarioNormal)
	for _, id := range got {
		if !easy[id] && !normal[id] {
			t.Errorf("100 Mario normal gave course %d, which fails more than 74 percent of the time", id)
		}
	}

	if len(got) != 4 {
		t.Errorf("100 Mario normal gave %d courses, want 4", len(got))
	}
}

func TestACourseNeedsTenAttemptsBeforeItsFailureRateCounts(t *testing.T) {
	server := setUp(t)

	// * Never failed, but tried only 9 times
	unrated := addCourse(t, server.store, MinAttemptsForDifficulty-1, MinAttemptsForDifficulty-1)
	rated := addCourse(t, server.store, MinAttemptsForDifficulty, MinAttemptsForDifficulty)

	if got := recommended(t, 1, oneHundredMarioEasy); !sameIDs(got, rated) {
		t.Errorf("asking for one easy course gave %v, want only %d, which has been tried enough", got, rated)
	}

	if got := recommended(t, 2, oneHundredMarioEasy); !sameIDs(got, rated, unrated) {
		t.Errorf("asking for two gave %v, want %d and then %d", got, rated, unrated)
	}
}

func TestCoursesComeInRangeThenUnratedThenAny(t *testing.T) {
	server := setUp(t)

	inRange := addCourseFailing(t, server.store, 10)
	unrated := addCourse(t, server.store, 0, 0)
	hard := addCourseFailing(t, server.store, 99)

	t.Setenv("PN_SMM_LOG_LOOKUPS", "1")

	if got := recommended(t, 3, oneHundredMarioEasy); !sameIDs(got, inRange, unrated, hard) {
		t.Errorf("got %v, want %d, then %d, then %d", got, inRange, unrated, hard)
	}

	// * Asking for more than there are gives all there are
	if got := recommended(t, 50, oneHundredMarioEasy); len(got) != 3 {
		t.Errorf("asking for 50 gave %d courses, want all 3", len(got))
	}
}

func TestOneHundredMarioNeverComesUpShortWhenThereAreEnoughCourses(t *testing.T) {
	server := setUp(t)

	// * 100 Mario asks for 50 and can not start with fewer than 32. Few
	// * of these are easy, so most must come from the other difficulties
	for range 5 {
		addCourseFailing(t, server.store, 20)
	}

	for range 10 {
		addCourse(t, server.store, 3, 0)
	}

	for range 45 {
		addCourseFailing(t, server.store, 90)
	}

	for _, extraData := range []types.List[types.String]{oneHundredMarioEasy, oneHundredMarioNormal} {
		if got := recommended(t, 50, extraData); len(got) != 50 {
			t.Errorf("%v gave %d courses, want the 50 asked for", extraData, len(got))
		}
	}
}

func TestRecommendedCoursesForAnyDifficultyAreAnyCourses(t *testing.T) {
	server := setUp(t)

	for range 7 {
		addCourseFailing(t, server.store, 50)
	}

	if got := recommended(t, 5, courseWorldAll); len(got) != 5 {
		t.Errorf("got %d courses, want 5", len(got))
	}
}

func TestRecommendedCoursesAreNeverMoreThanOneSearchMayHold(t *testing.T) {
	server := setUp(t)

	for range store.MaxRandomCourses + 5 {
		addRankedCourse(t, server.store, object{ratingSlots: 1}, 0)
	}

	// * None of these has been tried, so a difficulty is made up from
	// * courses not yet rated and then courses of any difficulty
	for _, extraData := range []types.List[types.String]{courseWorldAll, oneHundredMarioEasy} {
		if got := recommended(t, store.MaxRandomCourses+50, extraData); len(got) != store.MaxRandomCourses {
			t.Errorf("%v gave %d courses, want no more than %d", extraData, len(got), store.MaxRandomCourses)
		}
	}
}

func TestRecommendedCoursesAreNoMoreThanAskedForWhenManyHaveNotBeenTriedEnough(t *testing.T) {
	server := setUp(t)

	inRange := addCourseFailing(t, server.store, 10)
	for range 5 {
		addCourse(t, server.store, 0, 0)
	}

	got := recommended(t, 3, oneHundredMarioEasy)
	if len(got) != 3 || got[0] != inRange {
		t.Errorf("got %v, want 3 courses starting with %d", got, inRange)
	}
}

// * As in the ranked searches, a length of 0 asks for as many courses
// * as a search hands out
func TestAskingForNoRecommendedCoursesGivesAsManyAsThereAre(t *testing.T) {
	server := setUp(t)

	addCourseFailing(t, server.store, 10)
	addCourse(t, server.store, 0, 0)

	for _, extraData := range []types.List[types.String]{courseWorldAll, oneHundredMarioEasy} {
		if got := recommended(t, 0, extraData); len(got) != 2 {
			t.Errorf("%v gave %d courses, want both", extraData, len(got))
		}
	}
}

func TestRecommendedCoursesFailWhenTheStoreDoes(t *testing.T) {
	server := setUp(t)

	addCourse(t, server.store, 0, 0)

	for name, try := range map[string]struct {
		faults    faultyStore
		extraData types.List[types.String]
	}{
		"any difficulty":        {faultyStore{failAnyCourses: true}, courseWorldAll},
		"courses in range":      {faultyStore{failRatedCourses: true}, oneHundredMarioEasy},
		"courses not yet rated": {faultyStore{failUnratedCourses: true}, oneHundredMarioEasy},
	} {
		faults := try.faults
		faults.Store = server.store
		globals.Store = &faults

		rmc, nexError := RecommendedCourseSearchObject(nil, from(1000), 1, searchParam(0, 50, 0), try.extraData)
		if nexError == nil {
			t.Errorf("%s: got a response, want an error", name)
			continue
		}

		wantError(t, rmc, nexError, nex.ResultCodes.DataStore.Unknown)
	}
}

func TestRecommendedCoursesStillSendWhatWasFoundWhenFillingUpFails(t *testing.T) {
	server := setUp(t)

	inRange := addCourseFailing(t, server.store, 10)
	addCourseFailing(t, server.store, 99)

	globals.Store = &faultyStore{Store: server.store, failAnyCourses: true}

	if got := recommended(t, 5, oneHundredMarioEasy); !sameIDs(got, inRange) {
		t.Errorf("got %v, want the one course found before filling up failed", got)
	}
}

// * With the archive's catalog, New Arrivals is the courses that just
// * arrived from the archive, the latest first, then random courses that
// * are here, each listed once
func TestNewArrivalsListsTheLatestArrivalsThenRandomCourses(t *testing.T) {
	server := setUp(t)

	var courses []types.UInt64
	for range 6 {
		courses = append(courses, addRankedCourse(t, server.store, object{}, 0))
	}

	brought := 0
	globals.ArchiveNewArrivals = func(context.Context) { brought++ }
	globals.ArchiveArrivals = func(limit int) []uint64 { return []uint64{uint64(courses[4]), uint64(courses[1])}[:min(limit, 2)] }
	t.Cleanup(func() { globals.ArchiveNewArrivals, globals.ArchiveArrivals = nil, nil })

	listed := dataIDsOf(search(t, LatestCourseSearchObject, datastore_super_mario_maker.MethodLatestCourseSearchObject, searchParam(0, 4, 0), extra()))

	if len(listed) != 4 || listed[0] != courses[4] || listed[1] != courses[1] {
		t.Fatalf("listed %v, want %d and %d first, then two more", listed, courses[4], courses[1])
	}

	if !sameIDs(listed[:2], courses[4], courses[1]) || listed[2] == courses[4] || listed[2] == courses[1] || listed[3] == courses[4] || listed[3] == courses[1] || listed[2] == listed[3] {
		t.Errorf("listed %v, want each course once", listed)
	}

	if brought != 1 {
		t.Errorf("brought in %d batches, want one for the first page", brought)
	}

	// * A later page is random courses, and brings nothing in
	if later := search(t, LatestCourseSearchObject, datastore_super_mario_maker.MethodLatestCourseSearchObject, searchParam(4, 4, 0), extra()); len(later) != 4 || brought != 1 {
		t.Errorf("a later page listed %d courses and brought in %d batches, want 4 and still one", len(later), brought)
	}
}
