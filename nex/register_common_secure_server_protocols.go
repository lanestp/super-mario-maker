package nex

import (
	"os"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastorecommon "github.com/PretendoNetwork/nex-protocols-common-go/v2/datastore"
	securecommon "github.com/PretendoNetwork/nex-protocols-common-go/v2/secure-connection"
	datastoresmm "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	secure "github.com/PretendoNetwork/nex-protocols-go/v2/secure-connection"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	nex_datastore_super_mario_maker "github.com/PretendoNetwork/super-mario-maker/nex/datastore/super-mario-maker"
)

func registerCommonSecureProtocols() {
	secureProtocol := secure.NewProtocol()
	globals.SecureEndpoint.RegisterServiceProtocol(secureProtocol)
	commonSecureProtocol := securecommon.NewCommonProtocol(secureProtocol)
	commonSecureProtocol.CreateReportDBRecord = func(pid types.PID, reportID types.UInt32, reportData types.QBuffer) error {
		return nil
	}

	smmDatastore := datastoresmm.NewProtocol(globals.SecureEndpoint)

	smmDatastore.GetObjectInfos = nex_datastore_super_mario_maker.GetObjectInfos
	smmDatastore.RateCustomRanking = nex_datastore_super_mario_maker.RateCustomRanking
	smmDatastore.GetCustomRankingByDataID = nex_datastore_super_mario_maker.GetCustomRankingByDataID
	smmDatastore.AddToBufferQueues = nex_datastore_super_mario_maker.AddToBufferQueues
	smmDatastore.GetBufferQueue = nex_datastore_super_mario_maker.GetBufferQueue
	smmDatastore.CompleteAttachFile = nex_datastore_super_mario_maker.CompleteAttachFile
	smmDatastore.PrepareAttachFile = nex_datastore_super_mario_maker.PrepareAttachFile
	smmDatastore.GetApplicationConfig = nex_datastore_super_mario_maker.GetApplicationConfig
	smmDatastore.GetCustomRanking = nex_datastore_super_mario_maker.GetCustomRanking
	smmDatastore.LatestCourseSearchObject = nex_datastore_super_mario_maker.LatestCourseSearchObject
	smmDatastore.BestScoreRateCourseSearchObject = nex_datastore_super_mario_maker.BestScoreRateCourseSearchObject
	smmDatastore.FollowingsLatestCourseSearchObject = nex_datastore_super_mario_maker.FollowingsLatestCourseSearchObject
	smmDatastore.RecommendedCourseSearchObject = nex_datastore_super_mario_maker.RecommendedCourseSearchObject
	smmDatastore.SuggestedCourseSearchObject = nex_datastore_super_mario_maker.SuggestedCourseSearchObject
	smmDatastore.UploadCourseRecord = nex_datastore_super_mario_maker.UploadCourseRecord
	smmDatastore.GetCourseRecord = nex_datastore_super_mario_maker.GetCourseRecord
	smmDatastore.GetApplicationConfigString = nex_datastore_super_mario_maker.GetApplicationConfigString
	smmDatastore.GetDeletionReason = nex_datastore_super_mario_maker.GetDeletionReason
	smmDatastore.GetMetasWithCourseRecord = nex_datastore_super_mario_maker.GetMetasWithCourseRecord
	smmDatastore.CheckRateCustomRankingCounter = nex_datastore_super_mario_maker.CheckRateCustomRankingCounter
	smmDatastore.CTRPickUpCourseSearchObject = nex_datastore_super_mario_maker.CTRPickUpCourseSearchObject

	globals.SecureEndpoint.RegisterServiceProtocol(smmDatastore)

	commonDataStoreProtocol := datastorecommon.NewCommonProtocol(smmDatastore)

	commonDataStoreProtocol.SetMinIOClient(globals.MinIOClient)
	commonDataStoreProtocol.S3Bucket = os.Getenv("PN_SMM_CONFIG_S3_BUCKET")
	commonDataStoreProtocol.SetDataKeyBase(os.Getenv("PN_SMM_CONFIG_S3_KEY_BASE"))

	commonDataStoreProtocol.GetObjectInfoByDataID = globals.Store.GetObjectInfoByDataID
	commonDataStoreProtocol.GetObjectInfoByPersistenceTargetWithPassword = globals.Store.GetObjectInfoByPersistenceTargetWithPassword
	commonDataStoreProtocol.GetObjectInfoByDataIDWithPassword = globals.Store.GetObjectInfoByDataIDWithPassword
	commonDataStoreProtocol.GetObjectOwnerByDataID = globals.Store.GetObjectOwnerByDataID
	commonDataStoreProtocol.GetObjectSizeByDataID = globals.Store.GetObjectSizeByDataID

	commonDataStoreProtocol.UpdateObjectPeriodByDataIDWithPassword = globals.Store.UpdateObjectPeriodByDataIDWithPassword
	commonDataStoreProtocol.UpdateObjectMetaBinaryByDataIDWithPassword = globals.Store.UpdateObjectMetaBinaryByDataIDWithPassword
	commonDataStoreProtocol.UpdateObjectDataTypeByDataIDWithPassword = globals.Store.UpdateObjectDataTypeByDataIDWithPassword
	commonDataStoreProtocol.UpdateObjectUploadCompletedByDataID = globals.Store.UpdateObjectUploadCompletedByDataID

	commonDataStoreProtocol.InitializeObjectByPreparePostParam = globals.Store.InitializeObjectByPreparePostParam
	commonDataStoreProtocol.InitializeObjectRatingWithSlot = globals.Store.InitializeObjectRatingWithSlot
	commonDataStoreProtocol.RateObjectWithPassword = globals.Store.RateObjectWithPassword
	commonDataStoreProtocol.DeleteObjectByDataID = globals.Store.DeleteObjectByDataID

	// * For working out why a game does not show something. Prints what
	// * a game asks for when it looks up objects, and what it is given
	if os.Getenv("PN_SMM_LOG_LOOKUPS") == "1" {
		commonDataStoreProtocol.OnAfterGetMetasMultipleParam = logMetaLookups
	}

	globals.DatastoreCommon = commonDataStoreProtocol
}

func logMetaLookups(_ nex.PacketInterface, params types.List[datastore_types.DataStoreGetMetaParam]) {
	for _, param := range params {
		var info datastore_types.DataStoreMetaInfo
		var nexError *nex.Error

		if param.DataID == 0 {
			info, nexError = globals.Store.GetObjectInfoByPersistenceTargetWithPassword(param.PersistenceTarget, param.AccessPassword)
		} else {
			info, nexError = globals.Store.GetObjectInfoByDataIDWithPassword(param.DataID, param.AccessPassword)
		}

		if nexError != nil {
			globals.Logger.Infof("GetMetasMultipleParam: data id %d, owner %d slot %d, options %d -> %s", param.DataID, param.PersistenceTarget.OwnerID, param.PersistenceTarget.PersistenceSlotID, param.ResultOption, nexError.Error())
			continue
		}

		globals.Logger.Infof("GetMetasMultipleParam: data id %d, owner %d slot %d, options %d -> object %d of type %d named %q, %d byte meta binary", param.DataID, param.PersistenceTarget.OwnerID, param.PersistenceTarget.PersistenceSlotID, param.ResultOption, info.DataID, info.DataType, string(info.Name), len(info.MetaBinary))
	}
}
