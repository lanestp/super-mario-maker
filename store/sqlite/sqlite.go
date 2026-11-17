// Package sqlite is the SQLite storage backend. It keeps everything in a
// single file and needs no database server, which suits small private servers
package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"

	_ "modernc.org/sqlite"
)

// Store is a store.Store backed by a SQLite database
type Store struct {
	db *sql.DB
}

var _ store.Store = (*Store)(nil)

const schema = `
CREATE TABLE IF NOT EXISTS objects (
	data_id INTEGER PRIMARY KEY AUTOINCREMENT,
	upload_completed INTEGER NOT NULL DEFAULT 0,
	deleted INTEGER NOT NULL DEFAULT 0,
	deletion_reason INTEGER NOT NULL DEFAULT 0,
	under_review INTEGER NOT NULL DEFAULT 0,
	owner INTEGER NOT NULL DEFAULT 0,
	size INTEGER NOT NULL DEFAULT 0,
	name TEXT NOT NULL DEFAULT '',
	data_type INTEGER NOT NULL DEFAULT 0,
	meta_binary BLOB,
	permission INTEGER NOT NULL DEFAULT 0,
	permission_recipients TEXT NOT NULL DEFAULT '[]',
	delete_permission INTEGER NOT NULL DEFAULT 0,
	delete_permission_recipients TEXT NOT NULL DEFAULT '[]',
	flag INTEGER NOT NULL DEFAULT 0,
	period INTEGER NOT NULL DEFAULT 0,
	refer_data_id INTEGER NOT NULL DEFAULT 0,
	tags TEXT NOT NULL DEFAULT '[]',
	persistence_slot_id INTEGER NOT NULL DEFAULT 0,
	extra_data TEXT NOT NULL DEFAULT '[]',
	access_password INTEGER NOT NULL DEFAULT 0,
	update_password INTEGER NOT NULL DEFAULT 0,
	creation_date INTEGER NOT NULL DEFAULT 0,
	update_date INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS objects_owner ON objects (owner, persistence_slot_id);

CREATE TABLE IF NOT EXISTS object_ratings (
	data_id INTEGER NOT NULL,
	slot INTEGER NOT NULL,
	flag INTEGER NOT NULL DEFAULT 0,
	internal_flag INTEGER NOT NULL DEFAULT 0,
	lock_type INTEGER NOT NULL DEFAULT 0,
	initial_value INTEGER NOT NULL DEFAULT 0,
	range_min INTEGER NOT NULL DEFAULT 0,
	range_max INTEGER NOT NULL DEFAULT 0,
	period_hour INTEGER NOT NULL DEFAULT 0,
	period_duration INTEGER NOT NULL DEFAULT 0,
	total_value INTEGER NOT NULL DEFAULT 0,
	count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (data_id, slot)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS object_custom_rankings (
	data_id INTEGER NOT NULL,
	application_id INTEGER NOT NULL,
	value INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (data_id, application_id)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS import_cursors (
	name TEXT PRIMARY KEY,
	position INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS import_attempts (
	data_id INTEGER PRIMARY KEY,
	outcome TEXT NOT NULL,
	detail TEXT NOT NULL,
	entry BLOB NOT NULL,
	tries INTEGER NOT NULL,
	last_tried INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS buffer_queues (
	data_id INTEGER NOT NULL,
	slot INTEGER NOT NULL,
	creation_date INTEGER NOT NULL,
	buffer BLOB NOT NULL,
	PRIMARY KEY (data_id, slot, buffer)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS accounts (
	pid INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL,
	username_lower TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL DEFAULT '',
	nex_password TEXT NOT NULL,
	mii_name TEXT NOT NULL DEFAULT '',
	mii_data BLOB,
	creation_date INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS account_tokens (
	token_hash TEXT PRIMARY KEY,
	pid INTEGER NOT NULL,
	kind TEXT NOT NULL,
	expires_at INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS course_records (
	data_id INTEGER NOT NULL,
	slot INTEGER NOT NULL,
	first_pid INTEGER NOT NULL,
	best_pid INTEGER NOT NULL,
	best_score INTEGER NOT NULL,
	creation_date INTEGER NOT NULL,
	update_date INTEGER NOT NULL,
	PRIMARY KEY (data_id, slot)
) WITHOUT ROWID;
`

// Open opens the SQLite database at path, creating the file and tables if
// they do not exist. Use ":memory:" for a database that is not saved
func Open(path string) (*Store, error) {
	// * WAL lets reads continue during a write, and the busy timeout
	// * makes a second writer wait instead of failing right away
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// * SQLite allows one writer at a time. A single connection keeps writes
	// * in order, and is required for ":memory:" where every connection
	// * would otherwise get its own empty database
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating tables: %w", err)
	}

	// * Makes the first created object get store.FirstDataID
	_, err = db.Exec(`INSERT INTO sqlite_sequence (name, seq) SELECT 'objects', ? WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name='objects')`, store.FirstDataID-1)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("setting the first DataID: %w", err)
	}

	// * Makes the first created account get store.FirstPID
	_, err = db.Exec(`INSERT INTO sqlite_sequence (name, seq) SELECT 'accounts', ? WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name='accounts')`, store.FirstPID-1)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("setting the first PID: %w", err)
	}

	return &Store{db: db}, nil
}

// DB returns the underlying database connection
func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) EnsureEventCourseMetadata(size uint64) error {
	now := toTimestamp(time.Now())

	// * Metadata file has DataType 50, is owned by the "Quazal Rendez-Vous"
	// * special account (PID 2), is readable by everyone and can only be
	// * deleted by its owner
	_, err := s.db.Exec(`INSERT INTO objects (
		data_id, upload_completed, owner, size, name, data_type, meta_binary,
		permission, delete_permission, flag, period, refer_data_id, creation_date, update_date
	) VALUES (?, 1, 2, ?, '', 50, x'', 0, 3, 0, 64306, 0, ?, ?)
	ON CONFLICT (data_id) DO UPDATE SET
		update_date = CASE WHEN objects.size != excluded.size THEN excluded.update_date ELSE objects.update_date END,
		size = excluded.size,
		delete_permission = 3`,
		store.EventCourseMetadataDataID, int64(size), now, now,
	)

	return err
}

// * Times are stored as microseconds since the unix epoch
func toTimestamp(t time.Time) int64 {
	return t.UnixMicro()
}

func fromTimestamp(timestamp int64) time.Time {
	return time.UnixMicro(timestamp).UTC()
}

func toJSON[T any](values []T) string {
	if values == nil {
		values = []T{}
	}

	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}

	return string(encoded)
}

func fromJSON[T any](encoded string) []T {
	values := []T{}

	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		return []T{}
	}

	return values
}

func unknownError(err error) *nex.Error {
	globals.Logger.Error(err.Error())
	// TODO - Send more specific errors?
	return nex.NewError(nex.ResultCodes.DataStore.Unknown, err.Error())
}

func notFound() *nex.Error {
	return nex.NewError(nex.ResultCodes.DataStore.NotFound, "Object not found")
}
