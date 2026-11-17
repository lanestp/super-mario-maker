// Package postgres is the Postgres storage backend
package postgres

import (
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_smm_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/database"
	datastore_db "github.com/PretendoNetwork/super-mario-maker/database/datastore"
	datastore_smm_db "github.com/PretendoNetwork/super-mario-maker/database/datastore/super-mario-maker"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

// Store is a store.Store backed by the Postgres connection in database.Postgres
type Store struct{}

var _ store.Store = Store{}

// Open connects to Postgres using PN_SMM_POSTGRES_URI and creates the tables
func Open() Store {
	database.ConnectPostgres()

	return Store{}
}

func (Store) InitializeObjectByPreparePostParam(ownerPID types.PID, param datastore_types.DataStorePreparePostParam) (uint64, *nex.Error) {
	return datastore_db.InitializeObjectByPreparePostParam(ownerPID, param)
}

func (Store) InitializeObjectByAttachFileParam(ownerPID types.PID, param datastore_smm_types.DataStoreAttachFileParam) (types.UInt64, *nex.Error) {
	return datastore_smm_db.InitializeObjectByAttachFileParam(ownerPID, param)
}

func (Store) IsObjectAvailable(dataID types.UInt64) *nex.Error {
	return datastore_db.IsObjectAvailable(dataID)
}

func (Store) IsObjectAvailableWithPassword(dataID, password types.UInt64) *nex.Error {
	return datastore_db.IsObjectAvailableWithPassword(dataID, password)
}

func (Store) GetObjectInfoByDataID(dataID types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error) {
	return datastore_db.GetObjectInfoByDataID(dataID)
}

func (Store) GetObjectInfoByDataIDWithPassword(dataID, password types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error) {
	return datastore_db.GetObjectInfoByDataIDWithPassword(dataID, password)
}

func (Store) GetObjectInfoByPersistenceTargetWithPassword(persistenceTarget datastore_types.DataStorePersistenceTarget, password types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error) {
	return datastore_db.GetObjectInfoByPersistenceTargetWithPassword(persistenceTarget, password)
}

func (Store) GetObjectOwnerByDataID(dataID types.UInt64) (uint32, *nex.Error) {
	return datastore_db.GetObjectOwnerByDataID(dataID)
}

func (Store) GetObjectSizeByDataID(dataID types.UInt64) (uint32, *nex.Error) {
	return datastore_db.GetObjectSizeByDataID(dataID)
}

func (Store) UpdateObjectPeriodByDataIDWithPassword(dataID types.UInt64, period types.UInt16, password types.UInt64) *nex.Error {
	return datastore_db.UpdateObjectPeriodByDataIDWithPassword(dataID, period, password)
}

func (Store) UpdateObjectMetaBinaryByDataIDWithPassword(dataID types.UInt64, metaBinary types.QBuffer, password types.UInt64) *nex.Error {
	return datastore_db.UpdateObjectMetaBinaryByDataIDWithPassword(dataID, metaBinary, password)
}

func (Store) UpdateObjectDataTypeByDataIDWithPassword(dataID types.UInt64, dataType types.UInt16, password types.UInt64) *nex.Error {
	return datastore_db.UpdateObjectDataTypeByDataIDWithPassword(dataID, dataType, password)
}

func (Store) UpdateObjectUploadCompletedByDataID(dataID types.UInt64, uploadCompleted bool) *nex.Error {
	return datastore_db.UpdateObjectUploadCompletedByDataID(dataID, uploadCompleted)
}

func (Store) DeleteObjectByDataID(dataID types.UInt64) *nex.Error {
	return datastore_db.DeleteObjectByDataID(dataID)
}

func (Store) InitializeObjectRatingWithSlot(dataID uint64, param datastore_types.DataStoreRatingInitParamWithSlot) *nex.Error {
	return datastore_db.InitializeObjectRatingWithSlot(dataID, param)
}

func (Store) GetObjectRatingsWithSlotByDataID(dataID types.UInt64) ([]datastore_types.DataStoreRatingInfoWithSlot, *nex.Error) {
	return datastore_db.GetObjectRatingsWithSlotByDataID(dataID)
}

func (Store) GetObjectRatingsWithSlotByDataIDWithPassword(dataID, password types.UInt64) ([]datastore_types.DataStoreRatingInfoWithSlot, *nex.Error) {
	return datastore_db.GetObjectRatingsWithSlotByDataIDWithPassword(dataID, password)
}

func (Store) RateObjectWithPassword(dataID types.UInt64, slot types.UInt8, ratingValue types.Int32, accessPassword types.UInt64) (datastore_types.DataStoreRatingInfo, *nex.Error) {
	return datastore_db.RateObjectWithPassword(dataID, slot, ratingValue, accessPassword)
}

func (Store) GetUserCourseObjectIDs(ownerPID types.PID) (types.List[types.UInt64], *nex.Error) {
	return datastore_smm_db.GetUserCourseObjectIDs(ownerPID)
}

func (Store) GetRandomCoursesWithLimit(limit int) (types.List[datastore_smm_types.DataStoreCustomRankingResult], *nex.Error) {
	return datastore_smm_db.GetRandomCoursesWithLimit(limit)
}

func (Store) GetCustomRankingsByDataIDs(applicationID types.UInt32, dataIDs types.List[types.UInt64]) types.List[datastore_smm_types.DataStoreCustomRankingResult] {
	return datastore_smm_db.GetCustomRankingsByDataIDs(applicationID, dataIDs)
}

func (Store) GetCustomRankingDataIDs(applicationID types.UInt32, offset, limit int) (types.List[types.UInt64], *nex.Error) {
	return datastore_smm_db.GetCustomRankingDataIDs(applicationID, offset, limit)
}

func (Store) GetLatestCourseObjectIDs(offset, limit int) (types.List[types.UInt64], *nex.Error) {
	return datastore_smm_db.GetLatestCourseObjectIDs(offset, limit)
}

func (Store) InsertOrUpdateCustomRanking(dataID types.UInt64, applicationID, score types.UInt32) *nex.Error {
	return datastore_smm_db.InsertOrUpdateCustomRanking(dataID, applicationID, score)
}

func (Store) GetCourseRecordByDataIDAndSlot(dataID types.UInt64, slot types.UInt8) (datastore_smm_types.DataStoreGetCourseRecordResult, *nex.Error) {
	return datastore_smm_db.GetCourseRecordByDataIDAndSlot(dataID, slot)
}

func (Store) InsertOrUpdateCourseRecord(dataID types.UInt64, slot types.UInt8, pid types.PID, score types.Int32) *nex.Error {
	return datastore_smm_db.InsertOrUpdateCourseRecord(dataID, slot, pid, score)
}

func (Store) GetBufferQueuesByDataIDAndSlot(dataID types.UInt64, slot types.UInt32) (types.List[types.QBuffer], *nex.Error) {
	return datastore_smm_db.GetBufferQueuesByDataIDAndSlot(dataID, slot)
}

func (Store) InsertOrUpdateBufferQueueData(dataID types.UInt64, slot types.UInt32, buffer types.QBuffer) *nex.Error {
	return datastore_smm_db.InsertOrUpdateBufferQueueData(dataID, slot, buffer)
}

func (Store) EnsureEventCourseMetadata(size uint64) error {
	database.EnsureEventCourseMetadata(size)

	return nil
}

func (Store) Close() error {
	return database.Postgres.Close()
}
