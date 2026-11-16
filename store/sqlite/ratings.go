package sqlite

import (
	"database/sql"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
)

func (s *Store) InitializeObjectRatingWithSlot(dataID uint64, param datastore_types.DataStoreRatingInitParamWithSlot) *nex.Error {
	_, err := s.db.Exec(`INSERT INTO object_ratings (
		data_id, slot, flag, internal_flag, lock_type, initial_value,
		range_min, range_max, period_hour, period_duration, total_value
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		int64(dataID),
		int64(param.Slot),
		int64(param.Param.Flag),
		int64(param.Param.InternalFlag),
		int64(param.Param.LockType),
		int64(param.Param.InitialValue),
		int64(param.Param.RangeMin),
		int64(param.Param.RangeMax),
		int64(param.Param.PeriodHour),
		int64(param.Param.PeriodDuration),
		int64(param.Param.InitialValue), // * Start the value off at the initial value
	)
	if err != nil {
		return unknownError(err)
	}

	return nil
}

func (s *Store) GetObjectRatingsWithSlotByDataID(dataID types.UInt64) ([]datastore_types.DataStoreRatingInfoWithSlot, *nex.Error) {
	nexError := s.IsObjectAvailable(dataID)
	if nexError != nil {
		return nil, nexError
	}

	return s.objectRatings(dataID)
}

func (s *Store) GetObjectRatingsWithSlotByDataIDWithPassword(dataID, password types.UInt64) ([]datastore_types.DataStoreRatingInfoWithSlot, *nex.Error) {
	nexError := s.IsObjectAvailableWithPassword(dataID, password)
	if nexError != nil {
		return nil, nexError
	}

	return s.objectRatings(dataID)
}

// * Gets the ratings of an object without checking if it is available
func (s *Store) objectRatings(dataID types.UInt64) (types.List[datastore_types.DataStoreRatingInfoWithSlot], *nex.Error) {
	ratings := types.NewList[datastore_types.DataStoreRatingInfoWithSlot]()

	rows, err := s.db.Query(`SELECT slot, total_value, count, initial_value FROM object_ratings WHERE data_id=? ORDER BY slot`, int64(dataID))
	if err != nil {
		return nil, unknownError(err)
	}

	defer rows.Close()

	for rows.Next() {
		var slot, totalValue, count, initialValue int64

		if err := rows.Scan(&slot, &totalValue, &count, &initialValue); err != nil {
			return nil, unknownError(err)
		}

		rating := datastore_types.NewDataStoreRatingInfoWithSlot()
		rating.Slot = types.Int8(slot)
		rating.Rating = datastore_types.NewDataStoreRatingInfo()
		rating.Rating.TotalValue = types.Int64(totalValue)
		rating.Rating.Count = types.UInt32(count)
		rating.Rating.InitialValue = types.Int64(initialValue)

		ratings = append(ratings, rating)
	}

	if err := rows.Err(); err != nil {
		return nil, unknownError(err)
	}

	return ratings, nil
}

func (s *Store) RateObjectWithPassword(dataID types.UInt64, slot types.UInt8, ratingValue types.Int32, accessPassword types.UInt64) (datastore_types.DataStoreRatingInfo, *nex.Error) {
	nexError := s.IsObjectAvailableWithPassword(dataID, accessPassword)
	if nexError != nil {
		return datastore_types.NewDataStoreRatingInfo(), nexError
	}

	var totalValue, count, initialValue int64

	err := s.db.QueryRow(`UPDATE object_ratings
		SET total_value=total_value+?, count=count+1
		WHERE data_id=? AND slot=?
		RETURNING total_value, count, initial_value`,
		int64(ratingValue), int64(dataID), int64(slot),
	).Scan(&totalValue, &count, &initialValue)
	if err != nil {
		// * If no data was returned, that means the slot
		// * was out of bounds since the object exists due
		// * to the check at the start of the function.
		// * This is an invalid argument
		if err == sql.ErrNoRows {
			return datastore_types.NewDataStoreRatingInfo(), nex.NewError(nex.ResultCodes.DataStore.InvalidArgument, "Invalid argument")
		}

		return datastore_types.NewDataStoreRatingInfo(), unknownError(err)
	}

	rating := datastore_types.NewDataStoreRatingInfo()
	rating.TotalValue = types.Int64(totalValue)
	rating.Count = types.UInt32(count)
	rating.InitialValue = types.Int64(initialValue)

	return rating, nil
}
