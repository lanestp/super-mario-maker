package database_test

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"testing"
	"time"

	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/database"
	"github.com/PretendoNetwork/super-mario-maker/database/dbtest"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/lib/pq"
)

type eventCourseMetadata struct {
	owner, size, dataType, deletePermission, period int
	updated                                         time.Time
}

func readEventCourseMetadata(t *testing.T) eventCourseMetadata {
	t.Helper()

	var object eventCourseMetadata

	err := database.Postgres.QueryRow(`SELECT owner, size, data_type, delete_permission, period, update_date FROM datastore.objects WHERE data_id=900000`).
		Scan(&object.owner, &object.size, &object.dataType, &object.deletePermission, &object.period, &object.updated)
	if err != nil {
		t.Fatal(err)
	}

	return object
}

func TestTheEventCourseMetadataObjectIsMadeWhenItIsNotThere(t *testing.T) {
	dbtest.Open(t)

	database.EnsureEventCourseMetadata(321)

	object := readEventCourseMetadata(t)

	// * Owned by the secure server's account, and only it may delete it
	if object.owner != 2 || object.size != 321 || object.dataType != 50 || object.deletePermission != 3 || object.period != 64306 {
		t.Errorf("got %+v", object)
	}
}

func TestTheEventCourseMetadataObjectIsLeftAsItIsWhenItsSizeIsTheSame(t *testing.T) {
	dbtest.Open(t)

	database.EnsureEventCourseMetadata(321)
	before := readEventCourseMetadata(t)

	database.EnsureEventCourseMetadata(321)
	after := readEventCourseMetadata(t)

	if after != before {
		t.Errorf("got %+v, want it left as %+v", after, before)
	}
}

func TestTheEventCourseMetadataObjectTakesTheSizeOfANewFile(t *testing.T) {
	dbtest.Open(t)

	database.EnsureEventCourseMetadata(321)
	before := readEventCourseMetadata(t)

	database.EnsureEventCourseMetadata(400)
	after := readEventCourseMetadata(t)

	if after.size != 400 || !after.updated.After(before.updated) {
		t.Errorf("got size %d updated %s, want 400 and updated after %s", after.size, after.updated, before.updated)
	}
}

func TestTheEventCourseMetadataObjectCanOnlyBeDeletedByItsOwnerAgain(t *testing.T) {
	dbtest.Open(t)

	database.EnsureEventCourseMetadata(321)

	// * Everyone may delete it on Nintendo's servers
	if _, err := database.Postgres.Exec(`UPDATE datastore.objects SET delete_permission=0 WHERE data_id=900000`); err != nil {
		t.Fatal(err)
	}

	database.EnsureEventCourseMetadata(321)

	if object := readEventCourseMetadata(t); object.deletePermission != 3 {
		t.Errorf("got delete permission %d, want 3", object.deletePermission)
	}
}

func TestMadeCoursesComeAfterTheReservedDataIDs(t *testing.T) {
	dbtest.Open(t)

	// * Making the tables again leaves them as they are
	database.CreateTables()

	var first int64
	if err := database.Postgres.QueryRow(`SELECT nextval('datastore.object_data_id_seq')`).Scan(&first); err != nil {
		t.Fatal(err)
	}

	if first != 940000 {
		t.Errorf("the first DataID handed out is %d, want 940000, after the official event courses", first)
	}
}

func TestConnectingToPostgresMakesTheTables(t *testing.T) {
	adminURI := dbtest.AdminURI(t)

	if globals.Logger == nil {
		globals.Logger = plogger.NewLogger()
	}

	admin, err := sql.Open("postgres", adminURI)
	if err != nil {
		t.Fatal(err)
	}

	defer admin.Close()

	suffix := make([]byte, 8)
	rand.Read(suffix)
	name := "smm_test_" + hex.EncodeToString(suffix)

	if _, err := admin.Exec(`CREATE DATABASE ` + pq.QuoteIdentifier(name)); err != nil {
		t.Fatal(err)
	}

	address, err := url.Parse(adminURI)
	if err != nil {
		t.Fatal(err)
	}

	address.Path = "/" + name

	previous := database.Postgres
	t.Cleanup(func() {
		database.Postgres.Close()
		database.Postgres = previous
		admin.Exec(`DROP DATABASE ` + pq.QuoteIdentifier(name))
	})

	t.Setenv("PN_SMM_POSTGRES_URI", address.String())

	database.ConnectPostgres()

	for _, table := range []string{"datastore.objects", "datastore.object_ratings", "datastore.object_custom_rankings"} {
		var found sql.NullString
		if err := database.Postgres.QueryRow(`SELECT to_regclass($1)::text`, table).Scan(&found); err != nil || !found.Valid {
			t.Errorf("%s was not made (%v)", table, err)
		}
	}
}
