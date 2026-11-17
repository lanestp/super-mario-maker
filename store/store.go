// Package store defines the storage the server needs, independent of the
// database behind it. Every backend must behave the same way: the tests in
// store/storetest are run against each of them
package store

import (
	"errors"
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_smm_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"time"
)

// MaxRandomCourses is the most courses a single random course search will return.
// The largest request a real client makes is 100 (Course World on the Wii U)
const MaxRandomCourses = 100

// EventCourseMetadataDataID is the reserved DataID of the event course metadata file,
// which Super Mario Maker requires to open Course World
const EventCourseMetadataDataID = 900000

// FirstDataID is the DataID given to the first object created. Official
// event courses use DataIDs up to 930050, so objects must start after that
const FirstDataID = 940000

// Store is the storage used by the server
type Store interface {
	AccountStore

	// * Objects
	InitializeObjectByPreparePostParam(ownerPID types.PID, param datastore_types.DataStorePreparePostParam) (uint64, *nex.Error)
	InitializeObjectByAttachFileParam(ownerPID types.PID, param datastore_smm_types.DataStoreAttachFileParam) (types.UInt64, *nex.Error)
	// ImportObject makes an object with a DataID of its own, such as a
	// course's ID on Nintendo's servers, made at the given time. It is not
	// marked as uploaded. It returns ErrObjectExists when the DataID is taken
	ImportObject(dataID uint64, ownerPID types.PID, param datastore_types.DataStorePreparePostParam, created time.Time) error
	IsObjectAvailable(dataID types.UInt64) *nex.Error
	IsObjectAvailableWithPassword(dataID, password types.UInt64) *nex.Error
	GetObjectInfoByDataID(dataID types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error)
	GetObjectInfoByDataIDWithPassword(dataID, password types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error)
	GetObjectInfoByPersistenceTargetWithPassword(persistenceTarget datastore_types.DataStorePersistenceTarget, password types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error)
	GetObjectOwnerByDataID(dataID types.UInt64) (uint32, *nex.Error)
	GetObjectSizeByDataID(dataID types.UInt64) (uint32, *nex.Error)
	UpdateObjectPeriodByDataIDWithPassword(dataID types.UInt64, period types.UInt16, password types.UInt64) *nex.Error
	UpdateObjectMetaBinaryByDataIDWithPassword(dataID types.UInt64, metaBinary types.QBuffer, password types.UInt64) *nex.Error
	UpdateObjectDataTypeByDataIDWithPassword(dataID types.UInt64, dataType types.UInt16, password types.UInt64) *nex.Error
	UpdateObjectUploadCompletedByDataID(dataID types.UInt64, uploadCompleted bool) *nex.Error
	DeleteObjectByDataID(dataID types.UInt64) *nex.Error

	// * Ratings
	InitializeObjectRatingWithSlot(dataID uint64, param datastore_types.DataStoreRatingInitParamWithSlot) *nex.Error
	GetObjectRatingsWithSlotByDataID(dataID types.UInt64) ([]datastore_types.DataStoreRatingInfoWithSlot, *nex.Error)
	GetObjectRatingsWithSlotByDataIDWithPassword(dataID, password types.UInt64) ([]datastore_types.DataStoreRatingInfoWithSlot, *nex.Error)
	RateObjectWithPassword(dataID types.UInt64, slot types.UInt8, ratingValue types.Int32, accessPassword types.UInt64) (datastore_types.DataStoreRatingInfo, *nex.Error)

	// * Super Mario Maker
	GetUserCourseObjectIDs(ownerPID types.PID) (types.List[types.UInt64], *nex.Error)
	GetRandomCoursesWithLimit(limit int) (types.List[datastore_smm_types.DataStoreCustomRankingResult], *nex.Error)
	// GetRandomCoursesByFailureRate picks courses at random, as
	// GetRandomCoursesWithLimit does, from those the filter allows
	GetRandomCoursesByFailureRate(limit int, filter FailureRateFilter) (types.List[datastore_smm_types.DataStoreCustomRankingResult], *nex.Error)
	GetCustomRankingsByDataIDs(applicationID types.UInt32, dataIDs types.List[types.UInt64]) types.List[datastore_smm_types.DataStoreCustomRankingResult]
	// GetCustomRankingDataIDs lists the objects ranked for an application
	// that can be handed out, highest score first. Objects with the same
	// score are listed lowest DataID first
	GetCustomRankingDataIDs(applicationID types.UInt32, offset, limit int) (types.List[types.UInt64], *nex.Error)
	// GetLatestCourseObjectIDs lists the courses shown in Course World,
	// newest first
	GetLatestCourseObjectIDs(offset, limit int) (types.List[types.UInt64], *nex.Error)
	InsertOrUpdateCustomRanking(dataID types.UInt64, applicationID, score types.UInt32) *nex.Error
	GetCourseRecordByDataIDAndSlot(dataID types.UInt64, slot types.UInt8) (datastore_smm_types.DataStoreGetCourseRecordResult, *nex.Error)
	InsertOrUpdateCourseRecord(dataID types.UInt64, slot types.UInt8, pid types.PID, score types.Int32) *nex.Error
	GetBufferQueuesByDataIDAndSlot(dataID types.UInt64, slot types.UInt32) (types.List[types.QBuffer], *nex.Error)
	InsertOrUpdateBufferQueueData(dataID types.UInt64, slot types.UInt32, buffer types.QBuffer) *nex.Error

	// EnsureEventCourseMetadata makes sure the event course metadata object
	// exists and records the given size, which is the size of its file in storage
	EnsureEventCourseMetadata(size uint64) error

	Close() error
}

// The rating slots a game keeps on a course, as seen when a real game
// played one: one play in slot 0, a clear in slot 2, attempts in slot 3
// and deaths in slot 4. Each holds a running total
const (
	RatingSlotPlays    = 0
	RatingSlotClears   = 2
	RatingSlotAttempts = 3
	RatingSlotFails    = 4
)

// FailureRateFilter picks courses by how often attempts at them fail,
// which is how a game asks for courses of a difficulty. A course's
// failure rate is the attempts that were not clears, as a whole percent
// of all its attempts, rounded down
type FailureRateFilter struct {
	// MinPercent and MaxPercent are the failure rates allowed, both included
	MinPercent int
	MaxPercent int
	// MinAttempts is how many attempts a course needs before its failure
	// rate counts. Courses with fewer are never picked by rate
	MinAttempts int
	// Unrated picks only the courses with fewer than MinAttempts
	// attempts instead, whatever their rate
	Unrated bool
}

// ImportedDataIDLimit is above every DataID Nintendo's servers handed
// out. Imported objects keep their DataIDs, which are below it, and once
// something has been imported, the server's own new objects are given
// DataIDs from here on, so that the two never meet
const ImportedDataIDLimit = 1_000_000_000

// ErrObjectExists is returned when importing an object with a DataID
// another object already has
var ErrObjectExists = errors.New("an object with this DataID already exists")
