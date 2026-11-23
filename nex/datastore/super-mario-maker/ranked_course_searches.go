package nex_datastore_super_mario_maker

import (
	"os"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_super_mario_maker "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker"
	datastore_super_mario_maker_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

// * Turns the range a game asks for into an offset and a length that
// * is never more than a search hands out at once
func searchRange(resultRange types.ResultRange) (int, int) {
	length := int(resultRange.Length)
	if length <= 0 || length > store.MaxRandomCourses {
		length = store.MaxRandomCourses
	}

	return int(resultRange.Offset), length
}

// * Removes what a game did not ask for from results. The database
// * loads everything, so the fields are taken away, not added
func filterRankingResults(results types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult], resultOption types.UInt8) {
	for i := range results {
		if resultOption&0x1 == 0 {
			results[i].MetaInfo.Tags = types.NewList[types.String]()
		}

		if resultOption&0x2 == 0 {
			results[i].MetaInfo.Ratings = types.NewList[datastore_types.DataStoreRatingInfoWithSlot]()
		}

		if resultOption&0x4 == 0 {
			results[i].MetaInfo.MetaBinary = types.NewQBuffer(nil)
		}

		if resultOption&0x20 == 0 {
			results[i].Score = 0
		}
	}
}

// GetCustomRanking lists the objects in a ranking, highest score first.
// The ranking of application 0 is the stars of courses.
//
// The condition a game can send, on the ratings of an object, is not
// applied
func GetCustomRanking(err error, packet nex.PacketInterface, callID uint32, param datastore_super_mario_maker_types.DataStoreGetCustomRankingParam) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		globals.Logger.Error(err.Error())
		return nil, nex.NewError(nex.ResultCodes.DataStore.Unknown, err.Error())
	}

	offset, length := searchRange(param.ResultRange)

	dataIDs, nexError := globals.Store.GetCustomRankingDataIDs(param.ApplicationID, offset, length)
	if nexError != nil {
		return nil, nexError
	}

	pRankingResult := globals.Store.GetCustomRankingsByDataIDs(param.ApplicationID, dataIDs)
	filterRankingResults(pRankingResult, param.ResultOption)

	pResults := make(types.List[types.QResult], 0, len(pRankingResult))
	for range pRankingResult {
		pResults = append(pResults, types.NewQResultSuccess(nex.ResultCodes.Core.Unknown))
	}

	if os.Getenv("PN_SMM_LOG_LOOKUPS") == "1" {
		globals.Logger.Infof("GetCustomRanking: gave %d objects for %s", len(pRankingResult), param.FormatToString(0))
	}

	rmcResponseStream := nex.NewByteStreamOut(globals.SecureServer.LibraryVersions, globals.SecureServer.ByteStreamSettings)

	pRankingResult.WriteTo(rmcResponseStream)
	pResults.WriteTo(rmcResponseStream)

	rmcResponse := nex.NewRMCSuccess(globals.SecureEndpoint, rmcResponseStream.Bytes())
	rmcResponse.ProtocolID = datastore_super_mario_maker.ProtocolID
	rmcResponse.MethodID = datastore_super_mario_maker.MethodGetCustomRanking
	rmcResponse.CallID = callID

	return rmcResponse, nil
}

// LatestCourseSearchObject lists courses, newest first. The filters a
// game can send are not applied
func LatestCourseSearchObject(err error, packet nex.PacketInterface, callID uint32, param datastore_types.DataStoreSearchParam, extraData types.List[types.String]) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		globals.Logger.Error(err.Error())
		return nil, nex.NewError(nex.ResultCodes.DataStore.Unknown, err.Error())
	}

	offset, length := searchRange(param.ResultRange)

	// * With the archive's catalog, nobody uploads any more, so New
	// * Arrivals is courses that just arrived from the archive: a batch
	// * of random ones is brought in each time it is opened, the latest
	// * are listed first, and random courses that are here fill the rest
	if globals.ArchiveArrivals != nil {
		dataIDs, nexError := newArrivals(offset, length)
		if nexError != nil {
			return nil, nexError
		}

		return courseSearchResponse("LatestCourseSearchObject", datastore_super_mario_maker.MethodLatestCourseSearchObject, callID, dataIDs, param, extraData), nil
	}

	dataIDs, nexError := globals.Store.GetLatestCourseObjectIDs(offset, length)
	if nexError != nil {
		return nil, nexError
	}

	return courseSearchResponse("LatestCourseSearchObject", datastore_super_mario_maker.MethodLatestCourseSearchObject, callID, dataIDs, param, extraData), nil
}

// * Courses just brought in from the archive, the latest first, then
// * random courses that are here. A later page is random courses only
func newArrivals(offset, length int) (types.List[types.UInt64], *nex.Error) {
	dataIDs := types.NewList[types.UInt64]()
	listed := map[types.UInt64]bool{}

	if offset == 0 {
		globals.NewArrivalsFromArchive()

		for _, id := range globals.ArchiveArrivals(length) {
			dataIDs = append(dataIDs, types.UInt64(id))
			listed[types.UInt64(id)] = true
		}
	}

	if len(dataIDs) < length {
		random, nexError := globals.Store.GetRandomCoursesWithLimit(length)
		if nexError != nil {
			return nil, nexError
		}

		for _, result := range random {
			if len(dataIDs) == length {
				break
			}

			if id := result.MetaInfo.DataID; !listed[id] {
				dataIDs = append(dataIDs, id)
				listed[id] = true
			}
		}
	}

	return dataIDs, nil
}

// BestScoreRateCourseSearchObject lists courses, most stars first.
//
// GUESS. What a real server orders these by is not known. The name
// suggests the best score in some rating, and stars are the score a
// course has. The filters a game can send are not applied
func BestScoreRateCourseSearchObject(err error, packet nex.PacketInterface, callID uint32, param datastore_types.DataStoreSearchParam, extraData types.List[types.String]) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		globals.Logger.Error(err.Error())
		return nil, nex.NewError(nex.ResultCodes.DataStore.Unknown, err.Error())
	}

	offset, length := searchRange(param.ResultRange)

	dataIDs, nexError := globals.Store.GetCustomRankingDataIDs(0, offset, length)
	if nexError != nil {
		return nil, nexError
	}

	return courseSearchResponse("BestScoreRateCourseSearchObject", datastore_super_mario_maker.MethodBestScoreRateCourseSearchObject, callID, dataIDs, param, extraData), nil
}

func courseSearchResponse(name string, methodID uint32, callID uint32, dataIDs types.List[types.UInt64], param datastore_types.DataStoreSearchParam, extraData types.List[types.String]) *nex.RMCMessage {
	pRankingResults := globals.Store.GetCustomRankingsByDataIDs(0, dataIDs)
	filterRankingResults(pRankingResults, param.ResultOption)

	if os.Getenv("PN_SMM_LOG_LOOKUPS") == "1" {
		globals.Logger.Infof("%s: gave %d courses for extra data %v and %s", name, len(pRankingResults), extraData, param.FormatToString(0))
	}

	rmcResponseStream := nex.NewByteStreamOut(globals.SecureServer.LibraryVersions, globals.SecureServer.ByteStreamSettings)

	pRankingResults.WriteTo(rmcResponseStream)

	rmcResponse := nex.NewRMCSuccess(globals.SecureEndpoint, rmcResponseStream.Bytes())
	rmcResponse.ProtocolID = datastore_super_mario_maker.ProtocolID
	rmcResponse.MethodID = methodID
	rmcResponse.CallID = callID

	return rmcResponse
}
