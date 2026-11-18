// Package e2e tests the server as a whole, by building and running it
// the way it is run for real. The tests need nothing installed: the
// database is SQLite and S3 is a fake inside the test
package e2e

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/accounts"
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

		arguments := []string{"build", "-o", binaryPath}

		// * With PN_SMM_E2E_COVERAGE set to a folder, the server is
		// * built to count which of its code runs, and writes the
		// * counts there when it stops (see scripts/coverage.sh)
		if coverageDirectory() != "" {
			arguments = append(arguments, "-cover", "-coverpkg=github.com/PretendoNetwork/super-mario-maker/...")
		}

		build := exec.Command("go", append(arguments, "..")...)
		if output, err := build.CombinedOutput(); err != nil {
			buildError = fmt.Errorf("building the server: %v\n%s", err, output)
		}
	})

	if buildError != nil {
		t.Fatal(buildError)
	}

	return binaryPath
}

// * Ports for the server are picked at random from below the range the
// * system hands out by itself (from 32768 on Linux, 49152 on macOS), so
// * a port picked here can not be given to another test's listener or
// * connection before the server takes it. Each port given out is
// * remembered, so one test run never hands out the same one twice
const (
	lowestTestPort  = 20000
	highestTestPort = 29999
)

var (
	portsMutex sync.Mutex
	portsGiven = map[int]bool{}
)

func freeUDPPort(t *testing.T) int {
	t.Helper()

	return freePort(t, func(port int) bool { return !udpPortInUse(port) })
}

func freeTCPPort(t *testing.T) int {
	t.Helper()

	return freePort(t, func(port int) bool {
		listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		if err != nil {
			return false
		}

		listener.Close()

		return true
	})
}

func freePort(t *testing.T, free func(port int) bool) int {
	t.Helper()

	portsMutex.Lock()
	defer portsMutex.Unlock()

	for range 1000 {
		port := lowestTestPort + rand.IntN(highestTestPort-lowestTestPort+1)

		if !portsGiven[port] && free(port) {
			portsGiven[port] = true
			return port
		}
	}

	t.Fatal("no free port found for the server")

	return 0
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
	AccountAPIPort     int
	DatabasePath       string
	S3                 *s3test.Server

	command *exec.Cmd
	output  *bytes.Buffer
	exited  chan error
	stopped chan struct{}
}

// * Starts the server with SQLite, its own accounts and the given S3
// * server, without waiting for it to be ready
func startServer(t *testing.T, s3 *s3test.Server) *server {
	t.Helper()

	return startServerWith(t, s3, false, nil)
}

// * Like startServer, but first calls setup with the database
// * the server will use, so that it can be filled in. With
// * filesThroughServer, games are sent to the server itself for course
// * files, and not to S3
func startServerWith(t *testing.T, s3 *s3test.Server, filesThroughServer bool, setup func(database *sqlite.Store)) *server {
	t.Helper()

	directory := t.TempDir()

	started := &server{
		AuthenticationPort: freeUDPPort(t),
		SecurePort:         freeUDPPort(t),
		GRPCPort:           freeTCPPort(t),
		AccountAPIPort:     freeTCPPort(t),
		DatabasePath:       filepath.Join(directory, "smm.db"),
		S3:                 s3,
		output:             &bytes.Buffer{},
		exited:             make(chan error, 1),
		stopped:            make(chan struct{}),
	}

	if setup != nil {
		if globals.Logger == nil {
			globals.Logger = plogger.NewLogger()
		}

		database, err := sqlite.Open(started.DatabasePath)
		if err != nil {
			t.Fatal(err)
		}

		setup(database)

		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
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
		"PN_SMM_ACCOUNT_HTTP_PORT=" + strconv.Itoa(started.AccountAPIPort),
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

	if filesThroughServer {
		started.command.Env = append(started.command.Env, "PN_SMM_FILE_BASE_URL=http://127.0.0.1:"+strconv.Itoa(started.AccountAPIPort))
	}

	if directory := coverageDirectory(); directory != "" {
		started.command.Env = append(started.command.Env, "GOCOVERDIR="+directory)
	}

	if err := started.command.Start(); err != nil {
		t.Fatalf("starting the server: %v", err)
	}

	go func() {
		err := started.command.Wait()
		close(started.stopped)
		started.exited <- err
	}()

	t.Cleanup(func() {
		// * Asked to stop first, so a server built with -cover can
		// * write its counts. Cut off if it has not stopped in time
		started.command.Process.Signal(syscall.SIGTERM)

		select {
		case <-started.stopped:
		case <-time.After(5 * time.Second):
			started.command.Process.Kill()
		}

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

		if udpPortInUse(s.AuthenticationPort) && udpPortInUse(s.SecurePort) && tcpPortInUse(s.GRPCPort) && tcpPortInUse(s.AccountAPIPort) {
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
	s3.Put(t, "courses/900000.bin", eventCourseMetadata)

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

	// * The file is in the bucket, but not under the key prefix the
	// * game is sent to when it downloads the file
	s3 := s3test.Start(t)
	s3.Put(t, "900000.bin", []byte("event course metadata"))

	started := startServer(t, s3)

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

// * Finds the text of the first XML element with the given name
func xmlField(t *testing.T, document, name string) string {
	t.Helper()

	decoder := xml.NewDecoder(strings.NewReader(document))

	for {
		token, err := decoder.Token()
		if err != nil {
			t.Fatalf("no <%s> in %s", name, document)
		}

		if start, ok := token.(xml.StartElement); ok && start.Name.Local == name {
			var text string
			if err := decoder.DecodeElement(&text, &start); err != nil {
				t.Fatal(err)
			}

			return text
		}
	}
}

// * Logs in to the running server's account API the way a console does, and
// * asks it where the game server is
func TestConsoleCanLogInAndFindTheGameServer(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the server")
	}

	s3 := s3test.Start(t)
	s3.Put(t, "courses/900000.bin", []byte("event course metadata"))

	var account store.Account

	started := startServerWith(t, s3, false, func(database *sqlite.Store) {
		var err error

		account, err = accounts.Create(database, "PlayerOne", "secret123", "")
		if err != nil {
			t.Fatal(err)
		}
	})

	started.waitUntilReady(t)

	accountAPI := "http://127.0.0.1:" + strconv.Itoa(started.AccountAPIPort)

	login, err := http.PostForm(accountAPI+"/v1/api/oauth20/access_token/generate", url.Values{
		"grant_type":    {"password"},
		"user_id":       {"PlayerOne"},
		"password":      {accounts.NintendoPasswordHash(account.PID, "secret123")},
		"password_type": {"hash"},
	})
	if err != nil {
		t.Fatal(err)
	}

	loginBody, _ := io.ReadAll(login.Body)
	login.Body.Close()

	if login.StatusCode != http.StatusOK {
		t.Fatalf("login gave status %d: %s", login.StatusCode, loginBody)
	}

	request, err := http.NewRequest(http.MethodGet, accountAPI+"/v1/api/provider/nex_token/@me?game_server_id=1018DB00", nil)
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set("Authorization", "Bearer "+xmlField(t, string(loginBody), "token"))

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	body, _ := io.ReadAll(response.Body)
	response.Body.Close()

	nexToken := string(body)

	// * It must point at the authentication server this same process is running
	if host, port := xmlField(t, nexToken, "host"), xmlField(t, nexToken, "port"); host != "127.0.0.1" || port != strconv.Itoa(started.AuthenticationPort) {
		t.Errorf("the game server was given as %s:%s, want 127.0.0.1:%d", host, port, started.AuthenticationPort)
	}

	if xmlField(t, nexToken, "nex_password") != account.NEXPassword || xmlField(t, nexToken, "pid") != strconv.Itoa(int(account.PID)) {
		t.Errorf("got the wrong account details: %s", nexToken)
	}
}

// * Where a server built with -cover writes its counts, from
// * PN_SMM_E2E_COVERAGE. Empty when coverage is not being measured
func coverageDirectory() string {
	directory := os.Getenv("PN_SMM_E2E_COVERAGE")
	if directory == "" {
		return ""
	}

	absolute, err := filepath.Abs(directory)
	if err != nil {
		return directory
	}

	return absolute
}
