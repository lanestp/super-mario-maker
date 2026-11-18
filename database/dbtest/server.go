package dbtest

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// * The Postgres version the tests run against
const postgresVersion = embeddedpostgres.V16

var (
	startOnce sync.Once
	started   *embeddedpostgres.EmbeddedPostgres
	startedAt string
	startErr  error
	runtime   string
)

// AdminURI is the address of a Postgres server the tests may create and
// drop databases on: PN_SMM_TEST_POSTGRES_URI when it is set, and
// otherwise one started for this package's tests
func AdminURI(t *testing.T) string {
	t.Helper()

	if uri := os.Getenv("PN_SMM_TEST_POSTGRES_URI"); uri != "" {
		return uri
	}

	startOnce.Do(start)

	if startErr != nil {
		t.Fatalf("starting a Postgres for the tests: %v", startErr)
	}

	return startedAt
}

// Main runs a package's tests and stops the Postgres they started, if
// they started one. Call it from the package's TestMain
func Main(m *testing.M) {
	// * Stopped on Ctrl-C too, so no Postgres is left running
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-interrupted
		stop()
		os.Exit(1)
	}()

	code := m.Run()
	stop()
	os.Exit(code)
}

func start() {
	runtime, startErr = os.MkdirTemp("", "smm-test-postgres-")
	if startErr != nil {
		return
	}

	binaries, err := sharedBinaries()
	if err != nil {
		startErr = err
		return
	}

	// * Until the binaries have been unpacked once, packages tested side
	// * by side take turns, so two never fetch or unpack them at once.
	// * After that, each starts its own Postgres without waiting
	unpacked := filepath.Join(binaries, "unpacked")

	if _, err := os.Stat(unpacked); err != nil {
		unlock, err := lockAcrossProcesses(binaries + ".lock")
		if err != nil {
			startErr = err
			return
		}

		defer unlock()

		if _, err := os.Stat(unpacked); err != nil {
			// * Whatever an unpacking that did not finish left
			os.RemoveAll(filepath.Join(binaries, "bin"))
			os.RemoveAll(filepath.Join(binaries, "lib"))
			os.RemoveAll(filepath.Join(binaries, "share"))

			if startErr = startOnFreePort(binaries); startErr == nil {
				startErr = os.WriteFile(unpacked, nil, 0o600)
			}

			return
		}
	}

	startErr = startOnFreePort(binaries)
}

// * Where the Postgres binaries are unpacked, once for every test run
func sharedBinaries() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}

	binaries := filepath.Join(cache, "smm-test-postgres", string(postgresVersion))

	return binaries, os.MkdirAll(binaries, 0o700)
}

// * A port that is free now can be taken before Postgres opens it, so a
// * start that fails is tried again on another port
func startOnFreePort(binaries string) error {
	var err error

	for range 3 {
		var port uint32

		if port, err = freePort(); err != nil {
			return err
		}

		server := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
			Version(postgresVersion).
			Port(port).
			BinariesPath(binaries).
			RuntimePath(filepath.Join(runtime, fmt.Sprint(port))).
			Logger(nil))

		if err = server.Start(); err == nil {
			started = server
			startedAt = fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/postgres?sslmode=disable", port)

			return nil
		}
	}

	return err
}

func stop() {
	if started != nil {
		started.Stop()
		started = nil
	}

	if runtime != "" {
		os.RemoveAll(runtime)
	}
}

func freePort() (uint32, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}

	defer listener.Close()

	return uint32(listener.Addr().(*net.TCPAddr).Port), nil
}

func lockAcrossProcesses(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}

	return func() {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}
