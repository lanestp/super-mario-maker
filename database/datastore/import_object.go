package datastore_db

import (
	"errors"
	"fmt"
	"time"

	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/database"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/lib/pq"
)

// ImportObject makes an object with a DataID of its own, made at the
// given time. It is made as any other object is, and then moved to the
// DataID, all in one transaction so that nothing sees it half made
func ImportObject(dataID uint64, ownerPID types.PID, param datastore_types.DataStorePreparePostParam, created time.Time) error {
	if dataID == 0 || dataID >= store.ImportedDataIDLimit {
		return fmt.Errorf("an imported object needs a DataID from 1 to %d", store.ImportedDataIDLimit-1)
	}

	var exists bool
	if err := database.Postgres.QueryRow(`SELECT EXISTS(SELECT 1 FROM datastore.objects WHERE data_id=$1)`, dataID).Scan(&exists); err != nil {
		return err
	}

	if exists {
		return store.ErrObjectExists
	}

	madeID, nexError := InitializeObjectByPreparePostParam(ownerPID, param)
	if nexError != nil {
		return nexError
	}

	_, err := database.Postgres.Exec(`UPDATE datastore.objects SET data_id=$1, creation_date=$2, update_date=$2 WHERE data_id=$3`, dataID, created, madeID)
	if err != nil {
		var pqError *pq.Error
		if errors.As(err, &pqError) && pqError.Code == "23505" {
			database.Postgres.Exec(`DELETE FROM datastore.objects WHERE data_id=$1`, madeID)
			return store.ErrObjectExists
		}

		return err
	}

	// * The server's own new objects carry on from ImportedDataIDLimit, so
	// * they are never given a DataID that is, or will be, imported
	_, err = database.Postgres.Exec(`SELECT setval('datastore.object_data_id_seq', GREATEST((SELECT last_value FROM datastore.object_data_id_seq), $1))`, int64(store.ImportedDataIDLimit-1))

	return err
}
