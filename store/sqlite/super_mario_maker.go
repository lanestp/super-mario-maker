package sqlite

import (
	"database/sql"
	"time"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_smm_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

func (s *Store) GetUserCourseObjectIDs(ownerPID types.PID) (types.List[types.UInt64], *nex.Error) {
	courseObjectIDs := types.NewList[types.UInt64]()

	// * Course objects seem to have data types > 2 and < 50.
	// * Data type 1 seems to be reserved for "maker" objects.
	// * Data type 2 seems to be reserved for objects
	// * created through "PrepareAttachFile".
	// * Data type 50 is reserved for the Event Courses metadata
	// * file, and data type 51 is reserved for event courses
	rows, err := s.db.Query(`SELECT data_id FROM objects
		WHERE owner=? AND data_type > 2 AND data_type < 50 AND upload_completed=1 AND deleted=0 AND under_review=0
		ORDER BY data_id`, int64(ownerPID))
	if err != nil {
		return nil, unknownError(err)
	}

	defer rows.Close()

	for rows.Next() {
		var dataID int64

		if err := rows.Scan(&dataID); err != nil {
			return nil, unknownError(err)
		}

		courseObjectIDs = append(courseObjectIDs, types.UInt64(dataID))
	}

	if err := rows.Err(); err != nil {
		return nil, unknownError(err)
	}

	return courseObjectIDs, nil
}

func (s *Store) GetRandomCoursesWithLimit(limit int) (types.List[datastore_smm_types.DataStoreCustomRankingResult], *nex.Error) {
	courses := types.NewList[datastore_smm_types.DataStoreCustomRankingResult]()

	if limit <= 0 {
		return courses, nil
	}

	limit = min(limit, store.MaxRandomCourses)

	// * This sorts every available course, which is fine for the
	// * amount of courses a server small enough to use SQLite has
	rows, err := s.db.Query(`SELECT `+prefixedMetaInfoColumns+`, ranking.value
		FROM objects object
		JOIN object_custom_rankings ranking ON ranking.data_id = object.data_id AND ranking.application_id = 0
		WHERE object.upload_completed=1 AND object.deleted=0 AND object.under_review=0
		ORDER BY RANDOM()
		LIMIT ?`, limit)
	if err != nil {
		return nil, unknownError(err)
	}

	for rows.Next() {
		var score int64

		metaInfo, err := scanMetaInfo(rows, &score)
		if err != nil {
			rows.Close()
			return nil, unknownError(err)
		}

		course := datastore_smm_types.NewDataStoreCustomRankingResult()
		course.Order = 0 // * Order is ALWAYS 0
		course.Score = types.UInt32(score)
		course.MetaInfo = metaInfo

		courses = append(courses, course)
	}

	err = rows.Err()

	// * Closed before loading ratings, as there is only one connection
	rows.Close()

	if err != nil {
		return nil, unknownError(err)
	}

	for i := range courses {
		ratings, nexError := s.objectRatings(courses[i].MetaInfo.DataID)
		if nexError != nil {
			return nil, nexError
		}

		courses[i].MetaInfo.Ratings = ratings
	}

	return courses, nil
}

const prefixedMetaInfoColumns = `object.data_id, object.owner, object.size, object.name, object.data_type, object.meta_binary,
	object.permission, object.permission_recipients, object.delete_permission, object.delete_permission_recipients,
	object.period, object.refer_data_id, object.flag, object.tags, object.creation_date, object.update_date`

func (s *Store) GetCustomRankingsByDataIDs(applicationID types.UInt32, dataIDs types.List[types.UInt64]) types.List[datastore_smm_types.DataStoreCustomRankingResult] {
	// * Unlike other methods which query for multiple objects,
	// * DataStoreSMM::GetCustomRankingByDataID *OMITS* objects
	// * from it's response if they could not be found, rather
	// * than using a zero-ed object and DataStore::NotFound as
	// * the Result. Because of this, all errors are just thrown
	// * away here and not sent back to the client.
	// *
	// * The input may contain duplicate DataIDs, which should
	// * result in duplicate results
	results := make(types.List[datastore_smm_types.DataStoreCustomRankingResult], 0, len(dataIDs))

	for _, dataID := range dataIDs {
		var value int64

		err := s.db.QueryRow(`SELECT value FROM object_custom_rankings WHERE data_id=? AND application_id=?`, int64(dataID), int64(applicationID)).Scan(&value)
		if err != nil {
			continue
		}

		objectInfo, nexError := s.GetObjectInfoByDataID(dataID)
		if nexError != nil {
			continue
		}

		result := datastore_smm_types.NewDataStoreCustomRankingResult()

		// * Order is always 0, for some reason
		result.Score = types.UInt32(value)
		result.MetaInfo = objectInfo

		results = append(results, result)
	}

	return results
}

func (s *Store) InsertOrUpdateCustomRanking(dataID types.UInt64, applicationID, score types.UInt32) *nex.Error {
	nexError := s.IsObjectAvailable(dataID)
	if nexError != nil {
		return nexError
	}

	_, err := s.db.Exec(`INSERT INTO object_custom_rankings (data_id, application_id, value) VALUES (?, ?, ?)
		ON CONFLICT (data_id, application_id) DO UPDATE SET value=object_custom_rankings.value+excluded.value`,
		int64(dataID), int64(applicationID), int64(score),
	)
	if err != nil {
		return unknownError(err)
	}

	return nil
}

func (s *Store) GetCourseRecordByDataIDAndSlot(dataID types.UInt64, slot types.UInt8) (datastore_smm_types.DataStoreGetCourseRecordResult, *nex.Error) {
	nexError := s.IsObjectAvailable(dataID)
	if nexError != nil {
		return datastore_smm_types.NewDataStoreGetCourseRecordResult(), nexError
	}

	var firstPID, bestPID, bestScore, createdDate, updatedDate int64

	err := s.db.QueryRow(`SELECT first_pid, best_pid, best_score, creation_date, update_date
		FROM course_records WHERE data_id=? AND slot=?`, int64(dataID), int64(slot),
	).Scan(&firstPID, &bestPID, &bestScore, &createdDate, &updatedDate)
	if err != nil {
		if err == sql.ErrNoRows {
			return datastore_smm_types.NewDataStoreGetCourseRecordResult(), notFound()
		}

		return datastore_smm_types.NewDataStoreGetCourseRecordResult(), unknownError(err)
	}

	courseRecord := datastore_smm_types.NewDataStoreGetCourseRecordResult()
	courseRecord.DataID = dataID
	courseRecord.Slot = slot
	courseRecord.FirstPID = types.PID(firstPID)
	courseRecord.BestPID = types.PID(bestPID)
	courseRecord.BestScore = types.Int32(bestScore)
	courseRecord.CreatedTime.FromTimestamp(fromTimestamp(createdDate))
	courseRecord.UpdatedTime.FromTimestamp(fromTimestamp(updatedDate))

	return courseRecord, nil
}

func (s *Store) InsertOrUpdateCourseRecord(dataID types.UInt64, slot types.UInt8, pid types.PID, score types.Int32) *nex.Error {
	nexError := s.IsObjectAvailable(dataID)
	if nexError != nil {
		return nexError
	}

	now := toTimestamp(time.Now())

	// * A lower score is a better score, as it is the time taken
	_, err := s.db.Exec(`INSERT INTO course_records (data_id, slot, first_pid, best_pid, best_score, creation_date, update_date)
		VALUES (?1, ?2, ?3, ?3, ?4, ?5, ?5)
		ON CONFLICT (data_id, slot) DO UPDATE SET
			best_pid = CASE WHEN course_records.best_score > ?4 THEN ?3 ELSE course_records.best_pid END,
			update_date = CASE WHEN course_records.best_score > ?4 THEN ?5 ELSE course_records.update_date END,
			best_score = CASE WHEN course_records.best_score > ?4 THEN ?4 ELSE course_records.best_score END`,
		int64(dataID), int64(slot), int64(pid), int64(score), now,
	)
	if err != nil {
		return unknownError(err)
	}

	return nil
}

func (s *Store) GetBufferQueuesByDataIDAndSlot(dataID types.UInt64, slot types.UInt32) (types.List[types.QBuffer], *nex.Error) {
	nexError := s.IsObjectAvailable(dataID)
	if nexError != nil {
		return nil, nexError
	}

	bufferQueues := types.NewList[types.QBuffer]()

	rows, err := s.db.Query(`SELECT buffer FROM buffer_queues WHERE data_id=? AND slot=? ORDER BY creation_date`, int64(dataID), int64(slot))
	if err != nil {
		return nil, unknownError(err)
	}

	defer rows.Close()

	for rows.Next() {
		var buffer []byte

		if err := rows.Scan(&buffer); err != nil {
			return nil, unknownError(err)
		}

		bufferQueues = append(bufferQueues, types.QBuffer(buffer))
	}

	if err := rows.Err(); err != nil {
		return nil, unknownError(err)
	}

	return bufferQueues, nil
}

func (s *Store) InsertOrUpdateBufferQueueData(dataID types.UInt64, slot types.UInt32, buffer types.QBuffer) *nex.Error {
	nexError := s.IsObjectAvailable(dataID)
	if nexError != nil {
		return nexError
	}

	// * Real server does not allow duplicate
	// * buffers to be in a given objects slot,
	// * even if the buffers were uploaded by
	// * different clients. Instead, it removes
	// * the older buffer and adds the newer one.
	// * Instead of that, we just update the
	// * creation time
	_, err := s.db.Exec(`INSERT INTO buffer_queues (data_id, slot, creation_date, buffer) VALUES (?, ?, ?, ?)
		ON CONFLICT (data_id, slot, buffer) DO UPDATE SET creation_date=excluded.creation_date`,
		int64(dataID), int64(slot), toTimestamp(time.Now()), []byte(buffer),
	)
	if err != nil {
		return unknownError(err)
	}

	return nil
}
