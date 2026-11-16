package postgres

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/PretendoNetwork/super-mario-maker/database"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/lib/pq"
)

// * Postgres error code for a unique constraint being violated
const uniqueViolation = "23505"

func (Store) CreateAccount(account store.NewAccount) (store.Account, error) {
	nexPassword, err := store.GenerateNEXPassword()
	if err != nil {
		return store.Account{}, err
	}

	miiData := account.MiiData
	if miiData == nil {
		miiData = []byte{}
	}

	var pid int64

	err = database.Postgres.QueryRow(`INSERT INTO account.accounts (username, username_lower, nex_password, mii_name, mii_data, creation_date)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING pid`,
		account.Username, strings.ToLower(account.Username), nexPassword, account.MiiName, miiData, time.Now(),
	).Scan(&pid)
	if err != nil {
		var pqError *pq.Error
		if errors.As(err, &pqError) && pqError.Code == uniqueViolation {
			return store.Account{}, store.ErrUsernameTaken
		}

		return store.Account{}, err
	}

	return Store{}.GetAccountByPID(uint32(pid))
}

func (Store) SetAccountPasswordHash(pid uint32, passwordHash string) error {
	result, err := database.Postgres.Exec(`UPDATE account.accounts SET password_hash=$1 WHERE pid=$2`, passwordHash, int64(pid))
	if err != nil {
		return err
	}

	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if updated == 0 {
		return store.ErrAccountNotFound
	}

	return nil
}

const accountColumns = `pid, username, password_hash, nex_password, mii_name, mii_data, creation_date`

func (Store) GetAccountByPID(pid uint32) (store.Account, error) {
	return scanAccount(database.Postgres.QueryRow(`SELECT `+accountColumns+` FROM account.accounts WHERE pid=$1`, int64(pid)))
}

func (Store) GetAccountByUsername(username string) (store.Account, error) {
	return scanAccount(database.Postgres.QueryRow(`SELECT `+accountColumns+` FROM account.accounts WHERE username_lower=$1`, strings.ToLower(username)))
}

func scanAccount(row *sql.Row) (store.Account, error) {
	var account store.Account
	var pid int64

	err := row.Scan(&pid, &account.Username, &account.PasswordHash, &account.NEXPassword, &account.MiiName, &account.MiiData, &account.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return store.Account{}, store.ErrAccountNotFound
		}

		return store.Account{}, err
	}

	account.PID = uint32(pid)

	return account, nil
}
