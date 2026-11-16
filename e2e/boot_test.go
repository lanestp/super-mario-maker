// Package e2e tests the server as a whole, by building and running it
// the way it is run for real. The tests need nothing installed: the
// database is SQLite and S3 is a fake inside the test
package e2e

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/internal/s3test"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

var (
	buildOnce  sync.Once
	binaryPath string
	buildError error
)

// * Builds the server once for all tests
func serverBinary(t *testing.T) string {
	t.Helper()

	buildOnce.Do(func() {
		directory, err := os.MkdirTemp("", "smm-e2e")
		if err != nil {
			buildError = err
			return
		}

		binaryPath = filepath.Join(directory, "server")

		build := exec.Command("go", "build", "-o", binaryPath, "..")
		if output, err := build.CombinedOutput(); err != nil {
			buildError = fmt.Errorf("building the server: %v\n%s", err, output)
		}
	})

	if buildError != nil {
		t.Fatal(buildError)
	}

	return binaryPath
}

func freeUDPPort(t *testing.T) int {
	t.Helper()

	connection, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}

	defer connection.Close()

	return connection.LocalAddr().(*net.UDPAddr).Port
}

func freeTCPPort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}

func udpPortInUse(port int) bool {
	connection, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		return true
	}

	connection.Close()

	return false
}

func tcpPortInUse(port int) bool {
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 200*time.Millisecond)
	if err != nil {
		return false
	}

	connection.Close()

	return true
}

// Server is a running server
type server struct {
	AuthenticationPort int
	SecurePort         int
	GRPCPort           int
	DatabasePath       string
	S3                 *s3test.Server

	command *exec.Cmd
	output  *bytes.Buffer
	exited  chan error
}

// * Starts the server with SQLite, its own accounts and the given S3
// * server, without waiting for it to be ready
func startServer(t *testing.T, s3 *s3test.Server) *server {
	t.Helper()

	directory := t.TempDir()

	started := &server{
		AuthenticationPort: freeUDPPort(t),
		SecurePort:         freeUDPPort(t),
		GRPCPort:           freeTCPPort(t),
		DatabasePath:       filepath.Join(directory, "smm.db"),
		S3:                 s3,
		output:             &bytes.Buffer{},
		exited:             make(chan error, 1),
	}

	started.command = exec.Command(serverBinary(t))
	started.command.Dir = directory
	started.command.Stdout = started.output
	started.command.Stderr = started.output
	started.command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + directory,
		"PN_SMM_DATABASE=sqlite",
		"PN_SMM_SQLITE_PATH=" + started.DatabasePath,
		"PN_SMM_ACCOUNTS=local",
		"PN_SMM_AUTHENTICATION_SERVER_PORT=" + strconv.Itoa(started.AuthenticationPort),
		"PN_SMM_SECURE_SERVER_HOST=127.0.0.1",
		"PN_SMM_SECURE_SERVER_PORT=" + strconv.Itoa(started.SecurePort),
		"PN_SMM_GRPC_SERVER_PORT=" + strconv.Itoa(started.GRPCPort),
		"PN_SMM_GRPC_API_KEY=test-api-key",
		"PN_SMM_CONFIG_S3_ENDPOINT=" + s3.Endpoint,
		"PN_SMM_CONFIG_S3_ACCESS_KEY=" + s3test.AccessKey,
		"PN_SMM_CONFIG_S3_ACCESS_SECRET=" + s3test.SecretKey,
		"PN_SMM_CONFIG_S3_SECURE=false",
		"PN_SMM_CONFIG_S3_BUCKET=" + s3test.Bucket,
		"PN_SMM_CONFIG_S3_KEY_BASE=courses",
	}

	if err := started.command.Start(); err != nil {
		t.Fatalf("starting the server: %v", err)
	}

	go func() { started.exited <- started.command.Wait() }()

	t.Cleanup(func() {
		started.command.Process.Kill()

		if t.Failed() {
			t.Logf("server output:\n%s", started.output.String())
		}
	})

	return started
}

// * Waits until the server is listening on all of its ports
func (s *server) waitUntilReady(t *testing.T) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case err := <-s.exited:
			t.Fatalf("the server stopped before it was ready: %v\n%s", err, s.output.String())
		default:
		}

		if udpPortInUse(s.AuthenticationPort) && udpPortInUse(s.SecurePort) && tcpPortInUse(s.GRPCPort) {
			return
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("the server was not listening on all of its ports after 30 seconds\n%s", s.output.String())
}

func TestServerBoots(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the server")
	}

	eventCourseMetadata := bytes.Repeat([]byte{0x5A}, 4321)

	s3 := s3test.Start(t)
	s3.Put(t, "900000.bin", eventCourseMetadata)

	started := startServer(t, s3)
	started.waitUntilReady(t)

	// * Starting must have recorded the event course metadata file in the database
	globals.Logger = plogger.NewLogger()

	database, err := sqlite.Open(started.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}

	defer database.Close()

	size, nexError := database.GetObjectSizeByDataID(store.EventCourseMetadataDataID)
	if nexError != nil {
		t.Fatalf("the event course metadata object was not created: %v", nexError)
	}

	if int(size) != len(eventCourseMetadata) {
		t.Errorf("the event course metadata object has size %d, want %d", size, len(eventCourseMetadata))
	}

	// * It must still be running a moment later, and not have crashed after starting to listen
	select {
	case err := <-started.exited:
		t.Fatalf("the server stopped after starting: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestServerRefusesToBootWithoutEventCourseMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the server")
	}

	// * The bucket exists but does not have 900000.bin in it
	started := startServer(t, s3test.Start(t))

	select {
	case err := <-started.exited:
		if err == nil {
			t.Errorf("the server stopped with a success exit code, want a failure\n%s", started.output.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the server kept running without the event course metadata file")
	}

	if !bytes.Contains(started.output.Bytes(), []byte("900000.bin")) {
		t.Errorf("the server did not say which file is missing:\n%s", started.output.String())
	}
}
