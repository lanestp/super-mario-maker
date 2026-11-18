package open_test

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/database"
	"github.com/PretendoNetwork/super-mario-maker/database/dbtest"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/open"
)

func TestTheDatabaseIsPostgresWhenNoneIsNamed(t *testing.T) {
	for value, want := range map[string]string{"": "postgres", " ": "postgres", "postgres": "postgres", "SQLite ": "sqlite"} {
		t.Setenv("PN_SMM_DATABASE", value)

		backend, err := open.Backend()
		if err != nil || backend != want {
			t.Errorf("PN_SMM_DATABASE=%q: got %q (%v), want %s", value, backend, err, want)
		}
	}
}

func TestADatabaseThatIsNotKnownIsRefused(t *testing.T) {
	t.Setenv("PN_SMM_DATABASE", "MySQL")

	if _, err := open.FromEnv(); err == nil || err.Error() != "PN_SMM_DATABASE is not a supported database. Expected postgres or sqlite, got mysql" {
		t.Errorf("got %v, want mysql refused", err)
	}
}

func TestPostgresNeedsAnAddress(t *testing.T) {
	t.Setenv("PN_SMM_DATABASE", "")
	t.Setenv("PN_SMM_POSTGRES_URI", " ")

	if _, err := open.FromEnv(); err == nil || err.Error() != "PN_SMM_POSTGRES_URI environment variable not set" {
		t.Errorf("got %v, want the address asked for", err)
	}
}

// * Something stored is there when the database is opened again
func storesAcrossOpens(t *testing.T) {
	t.Helper()

	first, err := open.FromEnv()
	if err != nil {
		t.Fatal(err)
	}

	if err := first.EnsureEventCourseMetadata(12); err != nil {
		t.Fatal(err)
	}

	first.Close()

	second, err := open.FromEnv()
	if err != nil {
		t.Fatal(err)
	}

	defer second.Close()

	info, nexError := second.GetObjectInfoByDataID(types.UInt64(store.EventCourseMetadataDataID))
	if nexError != nil || info.Size != 12 {
		t.Errorf("got size %d (%v) after opening again, want what was stored", info.Size, nexError)
	}
}

func TestSQLiteIsKeptInTheFileNamed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smm.db")

	t.Setenv("PN_SMM_DATABASE", "sqlite")
	t.Setenv("PN_SMM_SQLITE_PATH", " "+path+" ")

	storesAcrossOpens(t)

	if _, err := os.Stat(path); err != nil {
		t.Errorf("the database is not in %s: %v", path, err)
	}
}

func TestSQLiteIsKeptInTheWorkingDirectoryWhenNoFileIsNamed(t *testing.T) {
	directory := t.TempDir()

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { os.Chdir(previous) })

	t.Setenv("PN_SMM_DATABASE", "sqlite")
	t.Setenv("PN_SMM_SQLITE_PATH", "")

	storesAcrossOpens(t)

	if _, err := os.Stat(filepath.Join(directory, open.DefaultSQLitePath)); err != nil {
		t.Errorf("the database is not %s in the working directory: %v", open.DefaultSQLitePath, err)
	}
}

func TestASQLiteFileThatCanNotBeMadeIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "smm.db")

	t.Setenv("PN_SMM_DATABASE", "sqlite")
	t.Setenv("PN_SMM_SQLITE_PATH", path)

	if _, err := open.FromEnv(); err == nil || !strings.HasPrefix(err.Error(), "opening SQLite database "+path+": ") {
		t.Errorf("got %v, want the file named", err)
	}
}

// * Skipped unless PN_SMM_TEST_POSTGRES_URI is set. See the dbtest package
func TestPostgresIsOpenedAtTheAddressGiven(t *testing.T) {
	dbtest.Open(t)

	if globals.Logger == nil {
		globals.Logger = plogger.NewLogger()
	}

	var name string
	if err := database.Postgres.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}

	address, err := url.Parse(os.Getenv("PN_SMM_TEST_POSTGRES_URI"))
	if err != nil {
		t.Fatal(err)
	}

	address.Path = "/" + name

	// * FromEnv points database.Postgres at its own connection, which is
	// * closed before dbtest drops the database
	previous := database.Postgres
	t.Cleanup(func() {
		if database.Postgres != previous {
			database.Postgres.Close()
		}

		database.Postgres = previous
	})

	t.Setenv("PN_SMM_DATABASE", "postgres")
	t.Setenv("PN_SMM_POSTGRES_URI", address.String())

	opened, err := open.FromEnv()
	if err != nil {
		t.Fatal(err)
	}

	if err := opened.EnsureEventCourseMetadata(12); err != nil {
		t.Fatal(err)
	}

	// * Looked for through the test's own connection to the same database
	check, err := sql.Open("postgres", address.String())
	if err != nil {
		t.Fatal(err)
	}

	defer check.Close()

	var size int
	if err := check.QueryRow(`SELECT size FROM datastore.objects WHERE data_id=900000`).Scan(&size); err != nil || size != 12 {
		t.Errorf("got size %d (%v), want the object stored in the database named", size, err)
	}
}
