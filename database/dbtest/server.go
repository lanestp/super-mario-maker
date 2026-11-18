package dbtest

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
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
	binaries, err := sharedBinaries()
	if err != nil {
		startErr = err
		return
	}

	stopLeftovers(binaries)

	runtime, startErr = os.MkdirTemp("", runtimePrefix)
	if startErr != nil {
		return
	}

	// * Which test process this Postgres belongs to, so a later run can
	// * tell when it has been left behind
	if startErr = os.WriteFile(filepath.Join(runtime, ownerFile), []byte(strconv.Itoa(os.Getpid())), 0o600); startErr != nil {
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

const (
	runtimePrefix = "smm-test-postgres-"
	ownerFile     = "owner"
)

// * A test process killed outright (by a timeout, kill -9, or a mutation
// * tester) can not stop its Postgres, which keeps running and keeps its
// * shared memory. Enough of them and no Postgres can start. So each
// * start first stops those whose test process has gone, and removes
// * their files
func stopLeftovers(binaries string) {
	runtimes, _ := filepath.Glob(filepath.Join(os.TempDir(), runtimePrefix+"*"))

	for _, leftover := range runtimes {
		owner, err := os.ReadFile(filepath.Join(leftover, ownerFile))
		if err != nil {
			continue
		}

		pid, err := strconv.Atoi(string(owner))
		if err != nil || processIsRunning(pid) {
			continue
		}

		dataDirectories, _ := filepath.Glob(filepath.Join(leftover, "*", "data"))
		for _, data := range dataDirectories {
			exec.Command(filepath.Join(binaries, "bin", "pg_ctl"), "stop", "-D", data, "-m", "immediate").Run()
		}

		os.RemoveAll(leftover)
	}
}

func processIsRunning(pid int) bool {
	err := syscall.Kill(pid, 0)

	return err == nil || errors.Is(err, syscall.EPERM)
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
