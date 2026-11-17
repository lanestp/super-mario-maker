package datastore_smm_db

import (
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/super-mario-maker/database"
	"github.com/PretendoNetwork/super-mario-maker/globals"
)

// GetCustomRankingDataIDs lists the objects ranked for an application
// that can be handed out, highest score first. Objects with the same
// score are listed lowest DataID first
func GetCustomRankingDataIDs(applicationID types.UInt32, offset, limit int) (types.List[types.UInt64], *nex.Error) {
	return objectIDs(`SELECT object.data_id
		FROM datastore.objects object
		JOIN datastore.object_custom_rankings ranking ON ranking.data_id = object.data_id AND ranking.application_id = $1
		WHERE object.upload_completed = TRUE AND object.deleted = FALSE AND object.under_review = FALSE
		ORDER BY ranking.value DESC, object.data_id
		LIMIT $2 OFFSET $3`, uint32(applicationID), max(limit, 0), max(offset, 0))
}

// GetLatestCourseObjectIDs lists the courses shown in Course World,
// newest first
func GetLatestCourseObjectIDs(offset, limit int) (types.List[types.UInt64], *nex.Error) {
	// * The same courses GetRandomCoursesWithLimit picks from
	return objectIDs(`SELECT object.data_id
		FROM datastore.objects object
		JOIN datastore.object_custom_rankings ranking ON ranking.data_id = object.data_id AND ranking.application_id = 0
		WHERE object.upload_completed = TRUE AND object.deleted = FALSE AND object.under_review = FALSE
			AND object.data_type > 2 AND object.data_type < 50
		ORDER BY object.creation_date DESC, object.data_id DESC
		LIMIT $1 OFFSET $2`, max(limit, 0), max(offset, 0))
}

func objectIDs(query string, arguments ...any) (types.List[types.UInt64], *nex.Error) {
	ids := types.NewList[types.UInt64]()

	rows, err := database.Postgres.Query(query, arguments...)
	if err != nil {
		globals.Logger.Error(err.Error())
		return nil, nex.NewError(nex.ResultCodes.DataStore.Unknown, err.Error())
	}

	defer rows.Close()

	for rows.Next() {
		var dataID uint64

		if err := rows.Scan(&dataID); err != nil {
			globals.Logger.Error(err.Error())
			return nil, nex.NewError(nex.ResultCodes.DataStore.Unknown, err.Error())
		}

		ids = append(ids, types.UInt64(dataID))
	}

	if err := rows.Err(); err != nil {
		globals.Logger.Error(err.Error())
		return nil, nex.NewError(nex.ResultCodes.DataStore.Unknown, err.Error())
	}

	return ids, nil
}
