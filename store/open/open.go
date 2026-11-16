// Package open opens the storage backend chosen by the environment
package open

import (
	"fmt"
	"os"
	"strings"

	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/postgres"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

// DefaultSQLitePath is the SQLite database file used when PN_SMM_SQLITE_PATH is not set
const DefaultSQLitePath = "super-mario-maker.db"

// Backend returns the storage backend named by PN_SMM_DATABASE, which is postgres when not set
func Backend() (string, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("PN_SMM_DATABASE")))

	switch backend {
	case "":
		return "postgres", nil
	case "postgres", "sqlite":
		return backend, nil
	default:
		return "", fmt.Errorf("PN_SMM_DATABASE is not a supported database. Expected postgres or sqlite, got %s", backend)
	}
}

// FromEnv opens the storage backend named by PN_SMM_DATABASE.
// globals.Logger must be set before calling it
func FromEnv() (store.Store, error) {
	backend, err := Backend()
	if err != nil {
		return nil, err
	}

	if backend == "sqlite" {
		path := strings.TrimSpace(os.Getenv("PN_SMM_SQLITE_PATH"))
		if path == "" {
			path = DefaultSQLitePath
		}

		opened, err := sqlite.Open(path)
		if err != nil {
			return nil, fmt.Errorf("opening SQLite database %s: %w", path, err)
		}

		return opened, nil
	}

	if strings.TrimSpace(os.Getenv("PN_SMM_POSTGRES_URI")) == "" {
		return nil, fmt.Errorf("PN_SMM_POSTGRES_URI environment variable not set")
	}

	return postgres.Open(), nil
}
