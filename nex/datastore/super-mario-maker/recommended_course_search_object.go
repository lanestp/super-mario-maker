package nex_datastore_super_mario_maker

import (
	"os"
	"strconv"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_super_mario_maker "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker"
	datastore_super_mario_maker_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

func RecommendedCourseSearchObject(err error, packet nex.PacketInterface, callID uint32, param datastore_types.DataStoreSearchParam, extraData types.List[types.String]) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		globals.Logger.Error(err.Error())
		return nil, nex.NewError(nex.ResultCodes.DataStore.Unknown, err.Error())
	}

	// * This method is used in 100 Mario and Course World. The extra
	// * data says which difficulty is wanted, as a range of failure
	// * rates in percent, as seen from a real game:
	// *
	// * Course World (All)          ["",  "",   "",    "0", "0"]
	// * Course World (Easy)         ["1", "0",  "34",  "0", "0"]
	// * Course World (Normal)       ["1", "35", "74",  "0", "0"]
	// * Course World (Expert)       ["1", "75", "95",  "0", "0"]
	// * Course World (Super Expert) ["1", "96", "100", "0", "0"]
	// * 100 Mario (Easy)            ["1", "0",  "34",  "",  "0"]
	// * 100 Mario (Normal)          ["1", "0",  "74",  "",  "0"]

	// * 100 Mario requests 50 courses and needs at least 32 back,
	// * Course World on the Wii U requests 100. Asking for 0 asks for as
	// * many as a search hands out, as it does for the ranked searches
	length := int(param.ResultRange.Length)
	if length > store.MaxRandomCourses {
		globals.Logger.Warningf("Limiting request to %d courses (was %d)", store.MaxRandomCourses, length)
	}

	if length <= 0 || length > store.MaxRandomCourses {
		length = store.MaxRandomCourses
	}

	if os.Getenv("PN_SMM_LOG_LOOKUPS") == "1" {
		globals.Logger.Infof("RecommendedCourseSearchObject: %d courses asked for, extra data %q, minimal rating frequency %d", length, []types.String(extraData), param.MinimalRatingFrequency)
	}

	pRankingResults, nexError := recommendedCourses(length, extraData)
	if nexError != nil {
		return nil, nexError
	}

	rmcResponseStream := nex.NewByteStreamOut(globals.SecureServer.LibraryVersions, globals.SecureServer.ByteStreamSettings)

	pRankingResults.WriteTo(rmcResponseStream)

	rmcResponse := nex.NewRMCSuccess(globals.SecureEndpoint, rmcResponseStream.Bytes())
	rmcResponse.ProtocolID = datastore_super_mario_maker.ProtocolID
	rmcResponse.MethodID = datastore_super_mario_maker.MethodRecommendedCourseSearchObject
	rmcResponse.CallID = callID

	return rmcResponse, nil
}

// MinAttemptsForDifficulty is how many attempts a course needs before
// how often it is failed decides its difficulty. A course cleared on its
// only two attempts would otherwise count as the easiest there is
const MinAttemptsForDifficulty = 10

// DifficultyFilter reads the difficulty a game asks for from the extra
// data of a course search. It returns false when any difficulty will do
func DifficultyFilter(extraData types.List[types.String]) (store.FailureRateFilter, bool) {
	if len(extraData) < 3 || string(extraData[0]) != "1" {
		return store.FailureRateFilter{}, false
	}

	minPercent, minErr := strconv.Atoi(string(extraData[1]))
	maxPercent, maxErr := strconv.Atoi(string(extraData[2]))

	if minErr != nil || maxErr != nil || minPercent < 0 || maxPercent > 100 || minPercent > maxPercent {
		return store.FailureRateFilter{}, false
	}

	return store.FailureRateFilter{MinPercent: minPercent, MaxPercent: maxPercent, MinAttempts: MinAttemptsForDifficulty}, true
}

// * Picks courses of the difficulty asked for. When too few have been
// * tried enough to know their difficulty, courses that have not are
// * added, and then courses of any difficulty, as 100 Mario can not
// * start with too few
func recommendedCourses(length int, extraData types.List[types.String]) (types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult], *nex.Error) {
	filter, byDifficulty := DifficultyFilter(extraData)
	if !byDifficulty {
		return globals.Store.GetRandomCoursesWithLimit(length)
	}

	courses, nexError := globals.Store.GetRandomCoursesByFailureRate(length, filter)
	if nexError != nil {
		return nil, nexError
	}

	if len(courses) < length {
		filter.Unrated = true

		unrated, nexError := globals.Store.GetRandomCoursesByFailureRate(length-len(courses), filter)
		if nexError != nil {
			return nil, nexError
		}

		courses = append(courses, unrated...)
	}

	// * Still too few, as on a server with few courses of a difficulty:
	// * the rest are filled with courses of any difficulty, so that 100
	// * Mario still starts
	if len(courses) < length {
		courses = fillWithAnyCourses(courses, length)
	}

	return courses, nil
}

// * Adds courses picked at random, leaving out those already there, until
// * there are length courses or no more to add
func fillWithAnyCourses(courses types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult], length int) types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult] {
	others, nexError := globals.Store.GetRandomCoursesWithLimit(length)
	if nexError != nil {
		// * The courses already found are still worth sending
		return courses
	}

	picked := make(map[types.UInt64]bool, len(courses))
	for _, course := range courses {
		picked[course.MetaInfo.DataID] = true
	}

	for _, course := range others {
		if len(courses) >= length {
			break
		}

		if !picked[course.MetaInfo.DataID] {
			picked[course.MetaInfo.DataID] = true
			courses = append(courses, course)
		}
	}

	return courses
}
