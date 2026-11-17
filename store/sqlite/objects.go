package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_smm_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

func (s *Store) InitializeObjectByPreparePostParam(ownerPID types.PID, param datastore_types.DataStorePreparePostParam) (uint64, *nex.Error) {
	return s.insertObject(0, ownerPID, param, uint64(param.ReferDataID), time.Now())
}

func (s *Store) ImportObject(dataID uint64, ownerPID types.PID, param datastore_types.DataStorePreparePostParam, created time.Time) error {
	if dataID == 0 || dataID >= store.ImportedDataIDLimit {
		return fmt.Errorf("an imported object needs a DataID from 1 to %d", store.ImportedDataIDLimit-1)
	}

	_, nexError := s.insertObject(dataID, ownerPID, param, uint64(param.ReferDataID), created)
	if nexError != nil {
		if strings.Contains(nexError.Message, "UNIQUE constraint failed") {
			return store.ErrObjectExists
		}

		return nexError
	}

	// * The server's own new objects carry on from ImportedDataIDLimit
	_, err := s.db.Exec(`UPDATE sqlite_sequence SET seq = MAX(seq, ?) WHERE name = 'objects'`, int64(store.ImportedDataIDLimit-1))
	if err != nil {
		return err
	}

	return nil
}

func (s *Store) InitializeObjectByAttachFileParam(ownerPID types.PID, param datastore_smm_types.DataStoreAttachFileParam) (types.UInt64, *nex.Error) {
	dataID, nexError := s.insertObject(0, ownerPID, param.PostParam, uint64(param.ReferDataID), time.Now())

	return types.UInt64(dataID), nexError
}

func (s *Store) insertObject(givenDataID uint64, ownerPID types.PID, param datastore_types.DataStorePreparePostParam, referDataID uint64, created time.Time) (uint64, *nex.Error) {
	tags := make([]string, 0, len(param.Tags))
	for i := range param.Tags {
		tags = append(tags, string(param.Tags[i]))
	}

	extraData := make([]string, 0, len(param.ExtraData))
	for i := range param.ExtraData {
		extraData = append(extraData, string(param.ExtraData[i]))
	}

	now := toTimestamp(created)

	// * NULL makes SQLite pick the next DataID
	var given any
	if givenDataID != 0 {
		given = int64(givenDataID)
	}

	var dataID int64

	err := s.db.QueryRow(`INSERT INTO objects (
		data_id, owner, size, name, data_type, meta_binary,
		permission, permission_recipients, delete_permission, delete_permission_recipients,
		flag, period, refer_data_id, tags, persistence_slot_id, extra_data,
		creation_date, update_date
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING data_id`,
		given,
		int64(ownerPID),
		int64(param.Size),
		string(param.Name),
		int64(param.DataType),
		[]byte(param.MetaBinary),
		int64(param.Permission.Permission),
		toJSON(pidsToInts(param.Permission.RecipientIDs)),
		int64(param.DelPermission.Permission),
		toJSON(pidsToInts(param.DelPermission.RecipientIDs)),
		int64(param.Flag),
		int64(param.Period),
		int64(referDataID),
		toJSON(tags),
		int64(param.PersistenceInitParam.PersistenceSlotID), // TODO - Check param.PersistenceInitParam.DeleteLastObject?
		toJSON(extraData),
		now,
		now,
	).Scan(&dataID)
	if err != nil {
		return 0, unknownError(err)
	}

	return uint64(dataID), nil
}

func pidsToInts(pids types.List[types.PID]) []uint64 {
	ints := make([]uint64, 0, len(pids))
	for i := range pids {
		ints = append(ints, uint64(pids[i]))
	}

	return ints
}

func intsToPIDs(ints []uint64) types.List[types.PID] {
	pids := make(types.List[types.PID], 0, len(ints))
	for i := range ints {
		pids = append(pids, types.PID(ints[i]))
	}

	return pids
}

func (s *Store) IsObjectAvailable(dataID types.UInt64) *nex.Error {
	var underReview bool

	err := s.db.QueryRow(`SELECT under_review FROM objects WHERE data_id=? AND upload_completed=1 AND deleted=0`, int64(dataID)).Scan(&underReview)
	if err != nil {
		if err == sql.ErrNoRows {
			return notFound()
		}

		return unknownError(err)
	}

	if underReview {
		return nex.NewError(nex.ResultCodes.DataStore.UnderReviewing, "This object is currently under review")
	}

	return nil
}

func (s *Store) IsObjectAvailableWithPassword(dataID, password types.UInt64) *nex.Error {
	var underReview bool
	var accessPassword int64

	err := s.db.QueryRow(`SELECT under_review, access_password FROM objects WHERE data_id=? AND upload_completed=1 AND deleted=0`, int64(dataID)).Scan(&underReview, &accessPassword)
	if err != nil {
		if err == sql.ErrNoRows {
			return notFound()
		}

		return unknownError(err)
	}

	if accessPassword != 0 && uint64(accessPassword) != uint64(password) {
		return nex.NewError(nex.ResultCodes.DataStore.InvalidPassword, "Invalid password")
	}

	if underReview {
		return nex.NewError(nex.ResultCodes.DataStore.UnderReviewing, "This object is currently under review")
	}

	return nil
}

const metaInfoColumns = `data_id, owner, size, name, data_type, meta_binary,
	permission, permission_recipients, delete_permission, delete_permission_recipients,
	period, refer_data_id, flag, tags, creation_date, update_date`

type rowScanner interface {
	Scan(dest ...any) error
}

// * Scans metaInfoColumns, followed by any extra columns, into
// * a DataStoreMetaInfo. Ratings are not filled in
func scanMetaInfo(row rowScanner, extra ...any) (datastore_types.DataStoreMetaInfo, error) {
	metaInfo := datastore_types.NewDataStoreMetaInfo()
	metaInfo.Permission = datastore_types.NewDataStorePermission()
	metaInfo.DelPermission = datastore_types.NewDataStorePermission()
	metaInfo.ExpireTime = types.NewDateTime(0x9C3F3E0000) // * 9999-12-31T00:00:00.000Z. This is what the real server sends
	metaInfo.Ratings = types.NewList[datastore_types.DataStoreRatingInfoWithSlot]()

	var dataID, owner, size, dataType, permission, delPermission, period, referDataID, flag, createdDate, updatedDate int64
	var name, permissionRecipients, delPermissionRecipients, tags string
	var metaBinary []byte

	dest := append([]any{
		&dataID, &owner, &size, &name, &dataType, &metaBinary,
		&permission, &permissionRecipients, &delPermission, &delPermissionRecipients,
		&period, &referDataID, &flag, &tags, &createdDate, &updatedDate,
	}, extra...)

	if err := row.Scan(dest...); err != nil {
		return datastore_types.NewDataStoreMetaInfo(), err
	}

	metaInfo.DataID = types.UInt64(dataID)
	metaInfo.OwnerID = types.PID(owner)
	metaInfo.Size = types.UInt32(size)
	metaInfo.Name = types.String(name)
	metaInfo.DataType = types.UInt16(dataType)
	metaInfo.MetaBinary = types.QBuffer(metaBinary)
	metaInfo.Permission.Permission = types.UInt8(permission)
	metaInfo.Permission.RecipientIDs = intsToPIDs(fromJSON[uint64](permissionRecipients))
	metaInfo.DelPermission.Permission = types.UInt8(delPermission)
	metaInfo.DelPermission.RecipientIDs = intsToPIDs(fromJSON[uint64](delPermissionRecipients))
	metaInfo.Period = types.UInt16(period)
	metaInfo.ReferDataID = types.UInt32(referDataID)
	metaInfo.Flag = types.UInt32(flag)

	tagArray := fromJSON[string](tags)
	metaInfo.Tags = make(types.List[types.String], 0, len(tagArray))
	for i := range tagArray {
		metaInfo.Tags = append(metaInfo.Tags, types.String(tagArray[i]))
	}

	metaInfo.CreatedTime.FromTimestamp(fromTimestamp(createdDate))
	metaInfo.UpdatedTime.FromTimestamp(fromTimestamp(updatedDate))
	metaInfo.ReferredTime.FromTimestamp(fromTimestamp(createdDate)) // * This is what the real server does

	return metaInfo, nil
}

func (s *Store) GetObjectInfoByDataID(dataID types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error) {
	nexError := s.IsObjectAvailable(dataID)
	if nexError != nil {
		return datastore_types.NewDataStoreMetaInfo(), nexError
	}

	metaInfo, err := scanMetaInfo(s.db.QueryRow(`SELECT `+metaInfoColumns+` FROM objects WHERE data_id=?`, int64(dataID)))
	if err != nil {
		if err == sql.ErrNoRows {
			return datastore_types.NewDataStoreMetaInfo(), notFound()
		}

		return datastore_types.NewDataStoreMetaInfo(), unknownError(err)
	}

	ratings, nexError := s.objectRatings(dataID)
	if nexError != nil {
		return datastore_types.NewDataStoreMetaInfo(), nexError
	}

	metaInfo.Ratings = ratings

	return metaInfo, nil
}

func (s *Store) GetObjectInfoByDataIDWithPassword(dataID, password types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error) {
	nexError := s.IsObjectAvailableWithPassword(dataID, password)
	if nexError != nil {
		return datastore_types.NewDataStoreMetaInfo(), nexError
	}

	return s.GetObjectInfoByDataID(dataID)
}

func (s *Store) GetObjectInfoByPersistenceTargetWithPassword(persistenceTarget datastore_types.DataStorePersistenceTarget, password types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error) {
	var accessPassword int64
	var underReview bool

	// * The newest object is used if there are somehow multiple in the same slot
	metaInfo, err := scanMetaInfo(s.db.QueryRow(`SELECT `+metaInfoColumns+`, access_password, under_review
		FROM objects WHERE owner=? AND persistence_slot_id=? AND upload_completed=1 AND deleted=0
		ORDER BY data_id DESC LIMIT 1`,
		int64(persistenceTarget.OwnerID), int64(persistenceTarget.PersistenceSlotID),
	), &accessPassword, &underReview)
	if err != nil {
		if err == sql.ErrNoRows {
			return datastore_types.NewDataStoreMetaInfo(), notFound()
		}

		return datastore_types.NewDataStoreMetaInfo(), unknownError(err)
	}

	if accessPassword != 0 && uint64(accessPassword) != uint64(password) {
		return datastore_types.NewDataStoreMetaInfo(), nex.NewError(nex.ResultCodes.DataStore.InvalidPassword, "Invalid password")
	}

	if underReview {
		return datastore_types.NewDataStoreMetaInfo(), nex.NewError(nex.ResultCodes.DataStore.UnderReviewing, "This object is currently under review")
	}

	ratings, nexError := s.objectRatings(metaInfo.DataID)
	if nexError != nil {
		return datastore_types.NewDataStoreMetaInfo(), nexError
	}

	metaInfo.Ratings = ratings

	return metaInfo, nil
}

func (s *Store) GetObjectOwnerByDataID(dataID types.UInt64) (uint32, *nex.Error) {
	return s.objectUint32(`SELECT owner FROM objects WHERE data_id=?`, dataID)
}

func (s *Store) GetObjectSizeByDataID(dataID types.UInt64) (uint32, *nex.Error) {
	return s.objectUint32(`SELECT size FROM objects WHERE data_id=?`, dataID)
}

func (s *Store) objectUint32(query string, dataID types.UInt64) (uint32, *nex.Error) {
	var value int64

	err := s.db.QueryRow(query, int64(dataID)).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, notFound()
		}

		return 0, unknownError(err)
	}

	return uint32(value), nil
}

func (s *Store) UpdateObjectPeriodByDataIDWithPassword(dataID types.UInt64, period types.UInt16, password types.UInt64) *nex.Error {
	return s.updateObjectWithPassword(`UPDATE objects SET period=? WHERE data_id=?`, int64(period), dataID, password)
}

func (s *Store) UpdateObjectMetaBinaryByDataIDWithPassword(dataID types.UInt64, metaBinary types.QBuffer, password types.UInt64) *nex.Error {
	return s.updateObjectWithPassword(`UPDATE objects SET meta_binary=? WHERE data_id=?`, []byte(metaBinary), dataID, password)
}

func (s *Store) UpdateObjectDataTypeByDataIDWithPassword(dataID types.UInt64, dataType types.UInt16, password types.UInt64) *nex.Error {
	return s.updateObjectWithPassword(`UPDATE objects SET data_type=? WHERE data_id=?`, int64(dataType), dataID, password)
}

func (s *Store) updateObjectWithPassword(query string, value any, dataID, password types.UInt64) *nex.Error {
	var updatePassword int64
	var underReview bool

	err := s.db.QueryRow(`SELECT update_password, under_review FROM objects WHERE data_id=? AND upload_completed=1 AND deleted=0`, int64(dataID)).Scan(&updatePassword, &underReview)
	if err != nil {
		if err == sql.ErrNoRows {
			return notFound()
		}

		return unknownError(err)
	}

	if updatePassword != 0 && uint64(updatePassword) != uint64(password) {
		return nex.NewError(nex.ResultCodes.DataStore.InvalidPassword, "Invalid password")
	}

	if underReview {
		return nex.NewError(nex.ResultCodes.DataStore.UnderReviewing, "This object is under review")
	}

	if _, err := s.db.Exec(query, value, int64(dataID)); err != nil {
		return unknownError(err)
	}

	return nil
}

func (s *Store) UpdateObjectUploadCompletedByDataID(dataID types.UInt64, uploadCompleted bool) *nex.Error {
	var underReview bool

	err := s.db.QueryRow(`SELECT under_review FROM objects WHERE data_id=? AND deleted=0`, int64(dataID)).Scan(&underReview)
	if err != nil {
		if err == sql.ErrNoRows {
			return notFound()
		}

		return unknownError(err)
	}

	if underReview {
		return nex.NewError(nex.ResultCodes.DataStore.UnderReviewing, "This object is under review")
	}

	if _, err := s.db.Exec(`UPDATE objects SET upload_completed=? WHERE data_id=?`, uploadCompleted, int64(dataID)); err != nil {
		return unknownError(err)
	}

	return nil
}

func (s *Store) DeleteObjectByDataID(dataID types.UInt64) *nex.Error {
	nexError := s.IsObjectAvailable(dataID)
	if nexError != nil {
		return nexError
	}

	if _, err := s.db.Exec(`UPDATE objects SET deleted=1 WHERE data_id=?`, int64(dataID)); err != nil {
		return unknownError(err)
	}

	return nil
}
