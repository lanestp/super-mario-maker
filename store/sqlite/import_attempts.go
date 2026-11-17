package sqlite

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/PretendoNetwork/super-mario-maker/store"
)

func (s *Store) RecordImportAttempt(attempt store.ImportAttempt) error {
	_, err := s.db.Exec(`INSERT INTO import_attempts (data_id, outcome, detail, entry, tries, last_tried) VALUES (?, ?, ?, ?, 1, ?)
		ON CONFLICT (data_id) DO UPDATE SET outcome=excluded.outcome, detail=excluded.detail, entry=excluded.entry,
			tries=import_attempts.tries+1, last_tried=excluded.last_tried`,
		int64(attempt.DataID), attempt.Outcome, attempt.Detail, attempt.Entry, toTimestamp(time.Now()),
	)

	return err
}

func (s *Store) ImportAttemptsToRetry(outcomes []string, maxTries, limit int) ([]store.ImportAttempt, error) {
	if len(outcomes) == 0 || limit <= 0 {
		return nil, nil
	}

	args := make([]any, 0, len(outcomes)+2)
	for _, outcome := range outcomes {
		args = append(args, outcome)
	}

	args = append(args, maxTries, limit)

	rows, err := s.db.Query(`SELECT data_id, outcome, detail, entry, tries, last_tried FROM import_attempts
		WHERE outcome IN (?`+strings.Repeat(", ?", len(outcomes)-1)+`) AND tries < ?
		ORDER BY last_tried, data_id
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var attempts []store.ImportAttempt

	for rows.Next() {
		var attempt store.ImportAttempt
		var dataID, lastTried int64

		if err := rows.Scan(&dataID, &attempt.Outcome, &attempt.Detail, &attempt.Entry, &attempt.Tries, &lastTried); err != nil {
			return nil, err
		}

		attempt.DataID = uint64(dataID)
		attempt.LastTried = fromTimestamp(lastTried)
		attempts = append(attempts, attempt)
	}

	return attempts, rows.Err()
}

func (s *Store) ForgetImportAttempt(dataID uint64) error {
	_, err := s.db.Exec(`DELETE FROM import_attempts WHERE data_id=?`, int64(dataID))
	return err
}

func (s *Store) ImportCursor(name string) (int64, error) {
	var position int64

	err := s.db.QueryRow(`SELECT position FROM import_cursors WHERE name=?`, name).Scan(&position)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}

	return position, err
}

func (s *Store) SetImportCursor(name string, position int64) error {
	_, err := s.db.Exec(`INSERT INTO import_cursors (name, position) VALUES (?, ?)
		ON CONFLICT (name) DO UPDATE SET position=excluded.position`, name, position)

	return err
}
