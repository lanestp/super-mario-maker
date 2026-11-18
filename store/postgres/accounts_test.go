package postgres_test

import (
	"errors"
	"testing"

	"github.com/PretendoNetwork/super-mario-maker/database"
	"github.com/PretendoNetwork/super-mario-maker/database/dbtest"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/postgres"
)

// * Only a clash on a unique column means the name or PID is taken. Any
// * other database error must come back as it is, or a broken database
// * would tell a player to pick another name
func TestADatabaseErrorIsNotReportedAsATakenNameOrPID(t *testing.T) {
	dbtest.Open(t)

	if _, err := database.Postgres.Exec(`DROP TABLE account.accounts CASCADE`); err != nil {
		t.Fatal(err)
	}

	for _, account := range []store.NewAccount{{Username: "PlayerOne"}, {Username: "PlayerTwo", PID: 1712345678}} {
		_, err := postgres.Store{}.CreateAccount(account)

		if err == nil || errors.Is(err, store.ErrUsernameTaken) || errors.Is(err, store.ErrPIDTaken) {
			t.Errorf("creating %+v with no accounts table gave %v, want the database's own error", account, err)
		}
	}
}
