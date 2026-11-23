package nex_datastore_super_mario_maker

import (
	"fmt"
	"os"
	"time"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_super_mario_maker "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker"
	datastore_super_mario_maker_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	"github.com/PretendoNetwork/super-mario-maker/globals"
)

func GetObjectInfos(err error, packet nex.PacketInterface, callID uint32, dataIDs types.List[types.UInt64]) (*nex.RMCMessage, *nex.Error) {
	if err != nil {
		globals.Logger.Error(err.Error())
		return nil, nex.NewError(nex.ResultCodes.DataStore.Unknown, err.Error())
	}

	pInfos := types.NewList[datastore_super_mario_maker_types.DataStoreFileServerObjectInfo]()

	for i := range dataIDs {
		objectInfo, nexError := globals.ObjectInfoOrFromArchive(dataIDs[i], globals.Store.GetObjectInfoByDataID)
		if nexError != nil {
			return nil, nexError
		}

		bucket := os.Getenv("PN_SMM_CONFIG_S3_BUCKET")
		// * The same key the file is uploaded to and downloaded from elsewhere
		key := fmt.Sprintf("%s/%d.bin", os.Getenv("PN_SMM_CONFIG_S3_KEY_BASE"), objectInfo.DataID)

		URL, err := globals.Presigner.GetObject(bucket, key, time.Minute*15)
		if err != nil {
			globals.Logger.Error(err.Error())
			return nil, nex.NewError(nex.ResultCodes.DataStore.OperationNotAllowed, "Operation not allowed")
		}

		info := datastore_super_mario_maker_types.NewDataStoreFileServerObjectInfo()
		info.DataID = objectInfo.DataID
		info.GetInfo.URL = types.NewString(URL.String())
		info.GetInfo.Size = objectInfo.Size
		info.GetInfo.RootCACert = types.NewBuffer(globals.FileRootCA)
		info.GetInfo.DataID = objectInfo.DataID

		pInfos = append(pInfos, info)
	}

	rmcResponseStream := nex.NewByteStreamOut(globals.SecureServer.LibraryVersions, globals.SecureServer.ByteStreamSettings)

	pInfos.WriteTo(rmcResponseStream)

	rmcResponse := nex.NewRMCSuccess(globals.SecureEndpoint, rmcResponseStream.Bytes())
	rmcResponse.ProtocolID = datastore_super_mario_maker.ProtocolID
	rmcResponse.MethodID = datastore_super_mario_maker.MethodGetObjectInfos
	rmcResponse.CallID = callID

	return rmcResponse, nil
}
