package archivefetch

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/super-mario-maker/store"
	_ "modernc.org/sqlite"
)

// Catalog is the archive's database of every course: the SQLite file
// published as "Super Mario Maker 1 - Level Database"
// (https://archive.org/details/mario_maker_database, smm_data.db). It
// holds each course's name, maker, numbers and record, and each player's
// Mii, from a snapshot taken when the servers closed. It is only read
type Catalog struct {
	db *sql.DB
}

// OpenCatalog opens the catalog at path, read only
func OpenCatalog(path string) (*Catalog, error) {
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}).String())
	if err != nil {
		return nil, err
	}

	var courses int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM Course LIMIT 1)`).Scan(&courses); err != nil || courses == 0 {
		db.Close()

		if err == nil {
			err = errors.New("it holds no courses")
		}

		return nil, fmt.Errorf("%s is not the archive's course database: %w", path, err)
	}

	return &Catalog{db: db}, nil
}

// Close closes the catalog
func (c *Catalog) Close() error {
	return c.db.Close()
}

// Course is what the catalog has on a course, as an index entry with no
// address for its file. nil when it has no such course, or when it is an
// event course, which the server keeps apart
func (c *Catalog) Course(dataID uint64) (*Entry, error) {
	var (
		entry                          Entry
		uploaded                       int64
		event                          bool
		creatorName, creatorMii        sql.NullString
		stars                          sql.NullInt64
		recordMS, recordPID            sql.NullInt64
		recordHolderName, recordHolder sql.NullString
	)

	err := c.db.QueryRow(`SELECT course.course_id, course.is_event_course, course.upload_time, course.course_name,
			course.creator_pid, creator.nnid, creator.mii_data,
			course.user_plays, course.clears, course.total_attempts, course.stars,
			record.time_milliseconds, record.best_time_pid, holder.nnid, holder.mii_data
		FROM Course course
		LEFT JOIN Player creator ON creator.player_id = course.creator_pid
		LEFT JOIN WorldRecord record ON record.course_id = course.course_id
		LEFT JOIN Player holder ON holder.player_id = record.best_time_pid
		WHERE course.course_id = ?`, int64(dataID)).Scan(
		&entry.LevelID, &event, &uploaded, &entry.Name,
		&entry.CreatorID, &creatorName, &creatorMii,
		&entry.UserPlays, &entry.Clears, &entry.TotalAttempts, &stars,
		&recordMS, &recordPID, &recordHolderName, &recordHolder,
	)

	if errors.Is(err, sql.ErrNoRows) || (err == nil && event) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	// * Times are kept as NEX date-times
	entry.UploadTime = types.NewDateTime(uint64(uploaded)).Standard().UTC().Format(time.RFC3339Nano)
	entry.Creator = creatorName.String
	entry.CreatorMii = creatorMii.String
	entry.Stars = int(stars.Int64)

	if recordMS.Valid && recordMS.Int64 > 0 && recordHolderName.Valid {
		entry.RecordMS = int(recordMS.Int64)
		entry.RecordHolder = recordHolderName.String
		entry.RecordHolderPID = uint32(recordPID.Int64)
		entry.RecordHolderMii = recordHolder.String
	}

	return &entry, nil
}

// MostPlayed lists up to limit course IDs, most played first, skipping
// the first skip of them. Event courses, and IDs the server keeps for
// itself, are left out
func (c *Catalog) MostPlayed(skip int64, limit int) ([]uint64, error) {
	return c.ids(`SELECT course_id FROM Course
		WHERE is_event_course = 0 AND course_id >= ? AND course_id < ?
		ORDER BY user_plays DESC, course_id DESC
		LIMIT ? OFFSET ?`, store.FirstDataID, store.ImportedDataIDLimit, limit, skip)
}

// Newest lists up to limit course IDs, the most recently uploaded first.
// Event courses, and IDs the server keeps for itself, are left out
func (c *Catalog) Newest(limit int) ([]uint64, error) {
	return c.ids(`SELECT course_id FROM Course
		WHERE is_event_course = 0 AND course_id >= ? AND course_id < ?
		ORDER BY upload_time DESC, course_id DESC
		LIMIT ?`, store.FirstDataID, store.ImportedDataIDLimit, limit)
}

func (c *Catalog) ids(query string, args ...any) ([]uint64, error) {
	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var ids []uint64

	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}

		ids = append(ids, uint64(id))
	}

	return ids, rows.Err()
}
