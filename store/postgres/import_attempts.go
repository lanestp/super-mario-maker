package postgres

import (
	"database/sql"
	"errors"
	"time"

	"github.com/PretendoNetwork/super-mario-maker/database"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/lib/pq"
)

func (Store) RecordImportAttempt(attempt store.ImportAttempt) error {
	entry := attempt.Entry
	if entry == nil {
		entry = []byte{}
	}

	_, err := database.Postgres.Exec(`INSERT INTO datastore.import_attempts (data_id, outcome, detail, entry, tries, last_tried) VALUES ($1, $2, $3, $4, 1, $5)
		ON CONFLICT (data_id) DO UPDATE SET outcome=excluded.outcome, detail=excluded.detail, entry=excluded.entry,
			tries=datastore.import_attempts.tries+1, last_tried=excluded.last_tried`,
		int64(attempt.DataID), attempt.Outcome, attempt.Detail, entry, time.Now(),
	)

	return err
}

func (Store) ImportAttemptsToRetry(outcomes []string, maxTries, limit int) ([]store.ImportAttempt, error) {
	if len(outcomes) == 0 || limit <= 0 {
		return nil, nil
	}

	rows, err := database.Postgres.Query(`SELECT data_id, outcome, detail, entry, tries, last_tried FROM datastore.import_attempts
		WHERE outcome = ANY($1) AND tries < $2
		ORDER BY last_tried, data_id
		LIMIT $3`, pq.Array(outcomes), maxTries, limit)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var attempts []store.ImportAttempt

	for rows.Next() {
		var attempt store.ImportAttempt
		var dataID int64

		if err := rows.Scan(&dataID, &attempt.Outcome, &attempt.Detail, &attempt.Entry, &attempt.Tries, &attempt.LastTried); err != nil {
			return nil, err
		}

		attempt.DataID = uint64(dataID)
		attempts = append(attempts, attempt)
	}

	return attempts, rows.Err()
}

func (Store) ForgetImportAttempt(dataID uint64) error {
	_, err := database.Postgres.Exec(`DELETE FROM datastore.import_attempts WHERE data_id=$1`, int64(dataID))
	return err
}

func (Store) ImportCursor(name string) (int64, error) {
	var position int64

	err := database.Postgres.QueryRow(`SELECT position FROM datastore.import_cursors WHERE name=$1`, name).Scan(&position)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}

	return position, err
}

func (Store) SetImportCursor(name string, position int64) error {
	_, err := database.Postgres.Exec(`INSERT INTO datastore.import_cursors (name, position) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET position=excluded.position`, name, position)

	return err
}
