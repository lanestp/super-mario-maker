// Package dbtest provides a throwaway Postgres database for tests.
//
// Tests which use it are skipped unless PN_SMM_TEST_POSTGRES_URI is set to the
// URI of a Postgres server the tests may create and drop databases on, such as
// postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable
package dbtest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/database"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/lib/pq"
)

// Open points database.Postgres at a new, empty database with all tables
// created. The database is dropped when the test finishes. Tests using it
// can not run in parallel, as database.Postgres is a global
func Open(t *testing.T) {
	t.Helper()

	adminURI := os.Getenv("PN_SMM_TEST_POSTGRES_URI")
	if adminURI == "" {
		t.Skip("PN_SMM_TEST_POSTGRES_URI not set, skipping database test")
	}

	if globals.Logger == nil {
		globals.Logger = plogger.NewLogger()
	}

	admin, err := sql.Open("postgres", adminURI)
	if err != nil {
		t.Fatalf("connecting to test Postgres server: %v", err)
	}

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}

	name := "smm_test_" + hex.EncodeToString(suffix)

	if _, err := admin.Exec(`CREATE DATABASE ` + pq.QuoteIdentifier(name)); err != nil {
		t.Fatalf("creating test database: %v", err)
	}

	testURI, err := url.Parse(adminURI)
	if err != nil {
		t.Fatalf("parsing PN_SMM_TEST_POSTGRES_URI: %v", err)
	}

	testURI.Path = "/" + name

	previous := database.Postgres

	database.Postgres, err = sql.Open("postgres", testURI.String())
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}

	t.Cleanup(func() {
		database.Postgres.Close()
		database.Postgres = previous

		admin.Exec(`DROP DATABASE ` + pq.QuoteIdentifier(name))
		admin.Close()
	})

	database.CreateTables()
}

// Course describes a course object to insert with InsertCourse.
// The zero value is an available course with no ratings
type Course struct {
	DataID           uint64
	Owner            uint32
	Name             string
	Score            uint32 // * Custom ranking value for application ID 0
	Deleted          bool
	UnderReview      bool
	UploadIncomplete bool
	NoRanking        bool    // * Skips the application ID 0 custom ranking, like a non-course object
	RatingTotals     []int64 // * One rating per slot, starting at slot 0
}

// InsertCourse inserts a course object, its application ID 0
// custom ranking and its ratings
func InsertCourse(t *testing.T, course Course) {
	t.Helper()

	now := time.Now()

	_, err := database.Postgres.Exec(`INSERT INTO datastore.objects (
		data_id, upload_completed, deleted, under_review, owner, size, name, data_type, meta_binary,
		permission, permission_recipients, delete_permission, delete_permission_recipients,
		flag, period, refer_data_id, tags, creation_date, update_date
	) VALUES ($1, $2, $3, $4, $5, 1024, $6, 6, $7, 0, '{}', 3, '{}', 0, 90, 0, $8, $9, $9)`,
		course.DataID,
		!course.UploadIncomplete,
		course.Deleted,
		course.UnderReview,
		course.Owner,
		course.Name,
		[]byte{0xAB, 0xCD},
		pq.Array([]string{"tag"}),
		now,
	)
	if err != nil {
		t.Fatalf("inserting course %d: %v", course.DataID, err)
	}

	if !course.NoRanking {
		_, err = database.Postgres.Exec(`INSERT INTO datastore.object_custom_rankings (data_id, application_id, value) VALUES ($1, 0, $2)`, course.DataID, course.Score)
		if err != nil {
			t.Fatalf("inserting custom ranking for course %d: %v", course.DataID, err)
		}
	}

	for slot, total := range course.RatingTotals {
		_, err = database.Postgres.Exec(`INSERT INTO datastore.object_ratings (data_id, slot, initial_value, total_value, count) VALUES ($1, $2, 0, $3, 1)`, course.DataID, slot, total)
		if err != nil {
			t.Fatalf("inserting rating slot %d for course %d: %v", slot, course.DataID, err)
		}
	}
}
