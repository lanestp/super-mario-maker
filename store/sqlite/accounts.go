package sqlite

import (
	"database/sql"
	"strings"
	"time"

	"github.com/PretendoNetwork/super-mario-maker/store"
)

func (s *Store) CreateAccount(account store.NewAccount) (store.Account, error) {
	nexPassword, err := store.GenerateNEXPassword()
	if err != nil {
		return store.Account{}, err
	}

	var pid int64

	err = s.db.QueryRow(`INSERT INTO accounts (username, username_lower, nex_password, mii_name, mii_data, creation_date)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING pid`,
		account.Username, strings.ToLower(account.Username), nexPassword, account.MiiName, account.MiiData, toTimestamp(time.Now()),
	).Scan(&pid)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return store.Account{}, store.ErrUsernameTaken
		}

		return store.Account{}, err
	}

	return s.GetAccountByPID(uint32(pid))
}

func (s *Store) SetAccountPasswordHash(pid uint32, passwordHash string) error {
	result, err := s.db.Exec(`UPDATE accounts SET password_hash=? WHERE pid=?`, passwordHash, int64(pid))
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

func (s *Store) GetAccountByPID(pid uint32) (store.Account, error) {
	return scanAccount(s.db.QueryRow(`SELECT `+accountColumns+` FROM accounts WHERE pid=?`, int64(pid)))
}

func (s *Store) GetAccountByUsername(username string) (store.Account, error) {
	return scanAccount(s.db.QueryRow(`SELECT `+accountColumns+` FROM accounts WHERE username_lower=?`, strings.ToLower(username)))
}

func scanAccount(row *sql.Row) (store.Account, error) {
	var account store.Account
	var pid, createdAt int64

	err := row.Scan(&pid, &account.Username, &account.PasswordHash, &account.NEXPassword, &account.MiiName, &account.MiiData, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return store.Account{}, store.ErrAccountNotFound
		}

		return store.Account{}, err
	}

	account.PID = uint32(pid)
	account.CreatedAt = fromTimestamp(createdAt)

	return account, nil
}
