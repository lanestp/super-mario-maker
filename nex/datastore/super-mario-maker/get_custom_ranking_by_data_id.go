package nex_datastore_super_mario_maker

import (
	"os"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_super_mario_maker "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker"
	datastore_super_mario_maker_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/globals"
)

func GetCustomRankingByDataID(err error, packet nex.PacketInterface, callID uint32, param datastore_super_mario_maker_types.DataStoreGetCustomRankingByDataIDParam) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		globals.Logger.Error(err.Error())
		return nil, nex.NewError(nex.ResultCodes.DataStore.Unknown, err.Error())
	}

	pRankingResult := globals.Store.GetCustomRankingsByDataIDs(param.ApplicationID, param.DataIDList)
	pResults := make(types.List[types.QResult], 0, len(param.DataIDList))

	if os.Getenv("PN_SMM_LOG_LOOKUPS") == "1" {
		globals.Logger.Infof("GetCustomRankingByDataID: found %d of %s", len(pRankingResult), param.FormatToString(0))
	}

	for i := range pRankingResult {
		// * This is kind of backwards.
		// * The database pulls this data
		// * by default, so it can be done
		// * in a single query. So instead
		// * of checking if a flag *IS*
		// * set, and conditionally *ADDING*
		// * the fields, we check if a flag
		// * is *NOT* set and conditionally
		// * *REMOVE* the field
		if param.ResultOption&0x1 == 0 {
			pRankingResult[i].MetaInfo.Tags = types.NewList[types.String]()
		}

		if param.ResultOption&0x2 == 0 {
			pRankingResult[i].MetaInfo.Ratings = types.NewList[datastore_types.DataStoreRatingInfoWithSlot]()
		}

		if param.ResultOption&0x4 == 0 {
			pRankingResult[i].MetaInfo.MetaBinary = types.NewQBuffer(nil)
		}

		if param.ResultOption&0x20 == 0 {
			pRankingResult[i].Score = 0
		}

		// * Since all errors are thrown away in
		// * globals.Store.GetCustomRankingsByDataIDs
		// * assume all objects returned were a success
		pResults = append(pResults, types.NewQResultSuccess(nex.ResultCodes.Core.Unknown))
	}

	rmcResponseStream := nex.NewByteStreamOut(globals.SecureServer.LibraryVersions, globals.SecureServer.ByteStreamSettings)

	pRankingResult.WriteTo(rmcResponseStream)
	pResults.WriteTo(rmcResponseStream)

	rmcResponse := nex.NewRMCSuccess(globals.SecureEndpoint, rmcResponseStream.Bytes())
	rmcResponse.ProtocolID = datastore_super_mario_maker.ProtocolID
	rmcResponse.MethodID = datastore_super_mario_maker.MethodGetCustomRankingByDataID
	rmcResponse.CallID = callID

	return rmcResponse, nil
}
