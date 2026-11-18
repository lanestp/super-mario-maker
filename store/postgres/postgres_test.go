package postgres_test

import (
	"testing"

	"github.com/PretendoNetwork/super-mario-maker/database"
	"github.com/PretendoNetwork/super-mario-maker/database/dbtest"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/postgres"
	"github.com/PretendoNetwork/super-mario-maker/store/storetest"
)

func TestStore(t *testing.T) {
	storetest.Run(t, storetest.Backend{
		Open: func(t *testing.T) store.Store {
			dbtest.Open(t)

			return testStore{}
		},
		SetUnderReview: func(t *testing.T, dataID uint64) {
			if _, err := database.Postgres.Exec(`UPDATE datastore.objects SET under_review=TRUE WHERE data_id=$1`, dataID); err != nil {
				t.Fatal(err)
			}
		},
		SetPasswords: func(t *testing.T, dataID uint64, access, update uint64) {
			if _, err := database.Postgres.Exec(`UPDATE datastore.objects SET access_password=$1, update_password=$2 WHERE data_id=$3`, int64(access), int64(update), int64(dataID)); err != nil {
				t.Fatal(err)
			}
		},
	})
}

// * dbtest closes and drops the database itself
type testStore struct {
	postgres.Store
}

func (testStore) Close() error {
	return nil
}
