package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/accountapi"
	"github.com/PretendoNetwork/super-mario-maker/fileproxy"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/internal/s3test"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

// * Every setting the server reads, so that a test starts from none of
// * them, whatever the machine running it has set
var settings = []string{
	"PN_SMM_ACCOUNTS",
	"PN_SMM_AUTHENTICATION_SERVER_PORT",
	"PN_SMM_SECURE_SERVER_HOST",
	"PN_SMM_SECURE_SERVER_PORT",
	"PN_SMM_GRPC_SERVER_PORT",
	"PN_SMM_GRPC_API_KEY",
	"PN_SMM_ACCOUNT_GRPC_HOST",
	"PN_SMM_ACCOUNT_GRPC_PORT",
	"PN_SMM_ACCOUNT_GRPC_API_KEY",
	"PN_SMM_ACCOUNT_HTTP_PORT",
	"PN_SMM_GAME_SERVER_IDS",
	"PN_SMM_ACCOUNT_HTTP_TLS_CERT",
	"PN_SMM_ACCOUNT_HTTP_TLS_KEY",
	"PN_SMM_ACCOUNT_HTTP_TLS_DOMAIN",
	"PN_SMM_ACCOUNT_HTTP_TLS_CACHE",
	"PN_SMM_ACCOUNT_HTTP_TLS_LEGACY",
	"PN_SMM_FILE_BASE_URL",
	"PN_SMM_FILE_URL_SECRET",
	"PN_SMM_FILE_ROOT_CA",
	"PN_SMM_FILE_HTTP_PORT",
	"PN_SMM_INVITE_CODE",
	"PN_SMM_ACCOUNT_PUBLIC_URL",
	"PN_SMM_CONSOLE_PLUGIN",
	"PN_SMM_IMPORT_TOKEN",
	"PN_SMM_IMPORT_ADDRESS",
	"PN_SMM_CONFIG_S3_ENDPOINT",
	"PN_SMM_CONFIG_S3_ACCESS_KEY",
	"PN_SMM_CONFIG_S3_ACCESS_SECRET",
	"PN_SMM_CONFIG_S3_SECURE",
	"PN_SMM_CONFIG_S3_REGION",
	"PN_SMM_CONFIG_S3_BUCKET",
	"PN_SMM_CONFIG_S3_KEY_BASE",
	"PN_SMM_DATABASE",
	"PN_SMM_SQLITE_PATH",
	"PN_SMM_POSTGRES_URI",
}

// * Clears every setting and puts back the globals the configure
// * functions change once the test is done
func startClean(t *testing.T) {
	t.Helper()

	for _, setting := range settings {
		t.Setenv(setting, "")
	}

	if globals.Logger == nil {
		globals.Logger = plogger.NewLogger()
	}

	accountAPI, presigner, rootCA, importServer := globals.AccountAPI, globals.Presigner, globals.FileRootCA, globals.ImportServer
	storeBefore, localAccounts, minIOClient := globals.Store, globals.LocalAccounts, globals.MinIOClient

	t.Cleanup(func() {
		globals.AccountAPI, globals.Presigner, globals.FileRootCA, globals.ImportServer = accountAPI, presigner, rootCA, importServer
		globals.Store, globals.LocalAccounts, globals.MinIOClient = storeBefore, localAccounts, minIOClient
	})

	globals.AccountAPI, globals.Presigner, globals.FileRootCA, globals.ImportServer = nil, nil, nil, nil
	globals.LocalAccounts = false
}

// * Sets the settings given as name, value pairs
func set(t *testing.T, pairs ...string) {
	t.Helper()

	for i := 0; i+1 < len(pairs); i += 2 {
		t.Setenv(pairs[i], pairs[i+1])
	}
}

func useStore(t *testing.T) store.Store {
	t.Helper()

	opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { opened.Close() })

	globals.Store = opened

	return opened
}

// * Runs a function and returns what it printed, which is where the
// * logger writes its lines
func printedBy(t *testing.T, run func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	previous := os.Stdout
	os.Stdout = writer

	printed := make(chan string)
	go func() {
		data, _ := io.ReadAll(reader)
		printed <- string(data)
	}()

	defer func() {
		os.Stdout = previous
	}()

	run()

	writer.Close()

	return <-printed
}

func wantError(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("got no error, want %q", want)
	}

	if err.Error() != want {
		t.Errorf("got error %q, want %q", err.Error(), want)
	}
}

// * The settings of an account API that can be served
func setAccountAPI(t *testing.T) {
	t.Helper()

	globals.LocalAccounts = true
	useStore(t)

	set(t,
		"PN_SMM_ACCOUNT_HTTP_PORT", "8443",
		"PN_SMM_SECURE_SERVER_HOST", "203.0.113.7",
		"PN_SMM_AUTHENTICATION_SERVER_PORT", "6000",
	)
}

func configuredAccountAPI(t *testing.T) *accountapi.Server {
	t.Helper()

	setAccountAPI(t)

	if err := configureAccountAPI(); err != nil {
		t.Fatal(err)
	}

	return globals.AccountAPI
}

func TestAccountsAreKeptOnTheAccountServerWhenNoSourceIsGiven(t *testing.T) {
	startClean(t)

	for value, wantLocal := range map[string]bool{"": false, "grpc": false, "local": true, " Local ": true, "GRPC": false} {
		set(t, "PN_SMM_ACCOUNTS", value)

		local, err := localAccountsFromEnv()
		if err != nil {
			t.Errorf("PN_SMM_ACCOUNTS=%q: %v", value, err)
		}

		if local != wantLocal {
			t.Errorf("PN_SMM_ACCOUNTS=%q: local accounts %v, want %v", value, local, wantLocal)
		}
	}
}

func TestAnAccountSourceThatIsNotKnownIsRefused(t *testing.T) {
	startClean(t)
	set(t, "PN_SMM_ACCOUNTS", "LDAP")

	_, err := localAccountsFromEnv()
	wantError(t, err, "PN_SMM_ACCOUNTS is not a supported account source. Expected grpc or local, got ldap")
}

func TestTheGameServerSettingsAreRequiredAndChecked(t *testing.T) {
	valid := []string{
		"PN_SMM_AUTHENTICATION_SERVER_PORT", "6000",
		"PN_SMM_SECURE_SERVER_HOST", "203.0.113.7",
		"PN_SMM_SECURE_SERVER_PORT", "6001",
		"PN_SMM_GRPC_SERVER_PORT", "50051",
	}

	cases := []struct {
		name    string
		changed []string
		want    string
	}{
		{"no authentication server port", []string{"PN_SMM_AUTHENTICATION_SERVER_PORT", " "}, "PN_SMM_AUTHENTICATION_SERVER_PORT environment variable not set"},
		{"an authentication server port that is not a number", []string{"PN_SMM_AUTHENTICATION_SERVER_PORT", "auth"}, "PN_SMM_AUTHENTICATION_SERVER_PORT is not a valid port. Expected 0-65535, got auth"},
		{"an authentication server port too high", []string{"PN_SMM_AUTHENTICATION_SERVER_PORT", "65536"}, "PN_SMM_AUTHENTICATION_SERVER_PORT is not a valid port. Expected 0-65535, got 65536"},
		{"a negative authentication server port", []string{"PN_SMM_AUTHENTICATION_SERVER_PORT", "-1"}, "PN_SMM_AUTHENTICATION_SERVER_PORT is not a valid port. Expected 0-65535, got -1"},
		{"no secure server host", []string{"PN_SMM_SECURE_SERVER_HOST", ""}, "PN_SMM_SECURE_SERVER_HOST environment variable not set"},
		{"no secure server port", []string{"PN_SMM_SECURE_SERVER_PORT", ""}, "PN_SMM_SECURE_SERVER_PORT environment variable not set"},
		{"a secure server port too high", []string{"PN_SMM_SECURE_SERVER_PORT", "70000"}, "PN_SMM_SECURE_SERVER_PORT is not a valid port. Expected 0-65535, got 70000"},
		{"no gRPC server port", []string{"PN_SMM_GRPC_SERVER_PORT", ""}, "PN_SMM_GRPC_SERVER_PORT is not a valid port. Expected 0-65535, got "},
		{"a gRPC server port that is not a number", []string{"PN_SMM_GRPC_SERVER_PORT", "grpc"}, "PN_SMM_GRPC_SERVER_PORT is not a valid port. Expected 0-65535, got grpc"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			startClean(t)
			set(t, valid...)
			set(t, c.changed...)

			_, err := gameServerPortsFromEnv()
			wantError(t, err, c.want)
		})
	}

	t.Run("all of them", func(t *testing.T) {
		startClean(t)
		set(t, valid...)

		port, err := gameServerPortsFromEnv()
		if err != nil {
			t.Fatal(err)
		}

		if port != 50051 {
			t.Errorf("got gRPC server port %d, want 50051", port)
		}
	})

	t.Run("the lowest and highest ports", func(t *testing.T) {
		for _, edge := range []string{"0", "65535"} {
			startClean(t)
			set(t, valid...)
			set(t, "PN_SMM_AUTHENTICATION_SERVER_PORT", edge, "PN_SMM_SECURE_SERVER_PORT", edge, "PN_SMM_GRPC_SERVER_PORT", edge)

			port, err := gameServerPortsFromEnv()
			if err != nil || strconv.Itoa(port) != edge {
				t.Errorf("ports of %s gave gRPC server port %d (%v), want them taken", edge, port, err)
			}
		}
	})
}

func TestTheAccountServerIsRequiredWhenAccountsAreKeptThere(t *testing.T) {
	cases := []struct {
		host, port, want string
	}{
		{"", "50051", "PN_SMM_ACCOUNT_GRPC_HOST environment variable not set"},
		{"account", "", "PN_SMM_ACCOUNT_GRPC_PORT environment variable not set"},
		{"account", "port", "PN_SMM_ACCOUNT_GRPC_PORT is not a valid port. Expected 0-65535, got port"},
		{"account", "99999", "PN_SMM_ACCOUNT_GRPC_PORT is not a valid port. Expected 0-65535, got 99999"},
	}

	for _, c := range cases {
		startClean(t)
		set(t, "PN_SMM_ACCOUNT_GRPC_HOST", c.host, "PN_SMM_ACCOUNT_GRPC_PORT", c.port)

		wantError(t, checkAccountServerFromEnv(), c.want)
	}

	startClean(t)
	set(t, "PN_SMM_ACCOUNT_GRPC_HOST", "account", "PN_SMM_ACCOUNT_GRPC_PORT", "50051")

	if err := checkAccountServerFromEnv(); err != nil {
		t.Errorf("a host and a port were refused: %v", err)
	}
}

func TestTheAccountAPIIsOnlyServedWhenItsPortIsSet(t *testing.T) {
	startClean(t)
	globals.LocalAccounts = true

	if err := configureAccountAPI(); err != nil {
		t.Fatal(err)
	}

	if globals.AccountAPI != nil {
		t.Error("the account API is served without PN_SMM_ACCOUNT_HTTP_PORT")
	}
}

func TestTheAccountAPINeedsLocalAccounts(t *testing.T) {
	startClean(t)
	setAccountAPI(t)
	globals.LocalAccounts = false

	wantError(t, configureAccountAPI(), "PN_SMM_ACCOUNT_HTTP_PORT is set, but the account API needs PN_SMM_ACCOUNTS to be local")

	if globals.AccountAPI != nil {
		t.Error("the refused account API is served")
	}
}

func TestTheAccountAPIRefusesAPortOutsideOneTo65535(t *testing.T) {
	for _, port := range []string{"0", "65536", "https"} {
		startClean(t)
		setAccountAPI(t)
		set(t, "PN_SMM_ACCOUNT_HTTP_PORT", port)

		wantError(t, configureAccountAPI(), "PN_SMM_ACCOUNT_HTTP_PORT is not a valid port. Expected 1-65535, got "+port)
	}
}

func TestTheAccountAPITakesThePortsOneAnd65535(t *testing.T) {
	for _, port := range []int{1, 65535} {
		startClean(t)
		setAccountAPI(t)
		set(t, "PN_SMM_ACCOUNT_HTTP_PORT", strconv.Itoa(port))

		if err := configureAccountAPI(); err != nil || globals.AccountAPI == nil || globals.AccountAPI.Port != port {
			t.Errorf("port %d was not taken: %v", port, err)
		}
	}
}

func TestTheAccountAPIAnswersForSuperMarioMakerInEveryRegionByDefault(t *testing.T) {
	startClean(t)

	server := configuredAccountAPI(t)

	if server.Port != 8443 {
		t.Errorf("served on port %d, want 8443", server.Port)
	}

	if strings.Join(server.Config.GameServerIDs, ",") != "1018DB00,1018DC00,1018DD00" {
		t.Errorf("answers for game servers %v, want Super Mario Maker's three", server.Config.GameServerIDs)
	}

	// * Consoles are sent to the authentication server
	if server.Config.GameServerHost != "203.0.113.7" || server.Config.GameServerPort != 6000 {
		t.Errorf("sends consoles to %s:%d, want 203.0.113.7:6000", server.Config.GameServerHost, server.Config.GameServerPort)
	}

	if server.Config.Accounts != globals.Store {
		t.Error("the account API does not use the server's store")
	}

	if server.LegacyTLS || server.TLSCertificatePath != "" || server.TLSDomain != "" {
		t.Error("TLS is set up without being asked for")
	}
}

func TestTheAccountAPIAnswersForTheGameServerIDsItIsGiven(t *testing.T) {
	startClean(t)
	setAccountAPI(t)
	set(t, "PN_SMM_GAME_SERVER_IDS", " 1018DB00 , 00000001")

	if err := configureAccountAPI(); err != nil {
		t.Fatal(err)
	}

	if ids := globals.AccountAPI.Config.GameServerIDs; strings.Join(ids, ",") != "1018DB00,00000001" {
		t.Errorf("answers for %q, want the two given without spaces", ids)
	}
}

func TestTheAccountAPIRefusesAGameServerIDThatIsNotEightHexadecimalDigits(t *testing.T) {
	startClean(t)
	setAccountAPI(t)
	set(t, "PN_SMM_GAME_SERVER_IDS", "1018DB00,mario")

	wantError(t, configureAccountAPI(), `The account API can not be served: game server IDs must be 8 hexadecimal digits, got "mario". The game server host comes from PN_SMM_SECURE_SERVER_HOST`)
}

func TestTheAccountAPIRefusesAHostNameForTheGameServer(t *testing.T) {
	startClean(t)
	setAccountAPI(t)
	set(t, "PN_SMM_SECURE_SERVER_HOST", "smm.example.com")

	wantError(t, configureAccountAPI(), `The account API can not be served: the game server host must be an IPv4 address, as consoles can not use a host name, got "smm.example.com". The game server host comes from PN_SMM_SECURE_SERVER_HOST`)
}

func TestTheAccountAPIRefusesLegacyTLSWithoutACertificate(t *testing.T) {
	startClean(t)
	setAccountAPI(t)
	set(t, "PN_SMM_ACCOUNT_HTTP_TLS_LEGACY", "true")

	wantError(t, configureAccountAPI(), "The account API can not be served over TLS: legacy TLS was asked for, but there is no certificate to serve TLS with")
}

func TestTheAccountAPIRefusesACertificateItCanNotRead(t *testing.T) {
	startClean(t)
	setAccountAPI(t)

	missing := filepath.Join(t.TempDir(), "missing.pem")
	set(t, "PN_SMM_ACCOUNT_HTTP_TLS_CERT", missing, "PN_SMM_ACCOUNT_HTTP_TLS_KEY", missing)

	err := configureAccountAPI()
	if err == nil || !strings.HasPrefix(err.Error(), "The account API can not be served over TLS: ") || !strings.Contains(err.Error(), "missing.pem") {
		t.Errorf("got %v, want the certificate refused at start", err)
	}
}

func TestTheAccountAPIOnlyAcceptsOldTLSWhenClearlyAskedTo(t *testing.T) {
	for value, want := range map[string]bool{"true": true, "1": true, " TRUE ": true, "yes": false, "on": false, "false": false} {
		startClean(t)
		setAccountAPI(t)

		// * A domain needs no certificate files, so legacy TLS can be asked for
		set(t, "PN_SMM_ACCOUNT_HTTP_TLS_DOMAIN", "smm.example.com", "PN_SMM_ACCOUNT_HTTP_TLS_LEGACY", value)

		if err := configureAccountAPI(); err != nil {
			t.Fatalf("PN_SMM_ACCOUNT_HTTP_TLS_LEGACY=%q: %v", value, err)
		}

		if globals.AccountAPI.LegacyTLS != want {
			t.Errorf("PN_SMM_ACCOUNT_HTTP_TLS_LEGACY=%q: legacy TLS %v, want %v", value, globals.AccountAPI.LegacyTLS, want)
		}

		if globals.AccountAPI.TLSDomain != "smm.example.com" {
			t.Errorf("got TLS domain %q", globals.AccountAPI.TLSDomain)
		}
	}
}

func TestFilesComeFromS3WhenNoFileAddressIsSet(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)

	if err := configureFileProxy(); err != nil {
		t.Fatal(err)
	}

	if globals.Presigner != nil || globals.AccountAPI.Files != nil {
		t.Error("files are passed through the server without PN_SMM_FILE_BASE_URL")
	}
}

func TestFilesThroughTheServerNeedTheAccountAPI(t *testing.T) {
	startClean(t)
	set(t, "PN_SMM_FILE_BASE_URL", "https://smm.example.com")

	wantError(t, configureFileProxy(), "PN_SMM_FILE_BASE_URL is set, but files are served on the account API's port, which needs PN_SMM_ACCOUNT_HTTP_PORT")
}

func TestAFileAddressThatIsNotAWebAddressIsRefused(t *testing.T) {
	for _, address := range []string{"smm.example.com", "ftp://smm.example.com", "https://", "://smm"} {
		startClean(t)
		configuredAccountAPI(t)
		set(t, "PN_SMM_FILE_BASE_URL", address)

		wantError(t, configureFileProxy(), "PN_SMM_FILE_BASE_URL is not a valid address. Expected something like https://smm.example.com, got "+address)
	}
}

func TestFilesAreServedOnTheAccountAPIsPort(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)
	set(t,
		"PN_SMM_FILE_BASE_URL", " http://203.0.113.7:8080 ",
		"PN_SMM_FILE_URL_SECRET", "the-secret",
		"PN_SMM_CONFIG_S3_BUCKET", "smm",
	)

	var err error
	printed := printedBy(t, func() { err = configureFileProxy() })

	if err != nil {
		t.Fatal(err)
	}

	proxy, ok := globals.Presigner.(*fileproxy.Proxy)
	if !ok {
		t.Fatalf("games are given links by %T, want the file proxy", globals.Presigner)
	}

	if proxy.BaseURL.String() != "http://203.0.113.7:8080" || string(proxy.Secret) != "the-secret" {
		t.Errorf("links go to %s and are signed with %q", proxy.BaseURL, proxy.Secret)
	}

	if storage, ok := proxy.Storage.(fileproxy.S3); !ok || storage.Bucket != "smm" {
		t.Errorf("files are kept in %#v, want the smm bucket", proxy.Storage)
	}

	if globals.AccountAPI.Files == nil || globals.AccountAPI.FilesPath != fileproxy.Path {
		t.Errorf("the account API serves files at %q", globals.AccountAPI.FilesPath)
	}

	if globals.AccountAPI.PlainFilesPort != 0 {
		t.Error("a port without TLS is opened without being asked for")
	}

	if !strings.Contains(printed, "Games upload and download files through this server, at http://203.0.113.7:8080/v1/files") {
		t.Errorf("did not say where files are served. Printed:\n%s", printed)
	}

	// * Served over HTTP, so a game needs no root certificate
	if strings.Contains(printed, "PN_SMM_FILE_ROOT_CA is not set") {
		t.Error("warned about the root certificate for files served over HTTP")
	}
}

func TestLinksToFilesAreSignedWithANewSecretEachStartWhenNoneIsGiven(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)
	set(t, "PN_SMM_FILE_BASE_URL", "http://203.0.113.7")

	secrets := [][]byte{}

	for range 2 {
		if err := configureFileProxy(); err != nil {
			t.Fatal(err)
		}

		secrets = append(secrets, globals.Presigner.(*fileproxy.Proxy).Secret)
	}

	if len(secrets[0]) != 32 || bytes.Equal(secrets[0], secrets[1]) {
		t.Errorf("got secrets %x and %x, want two different 32 byte secrets", secrets[0], secrets[1])
	}
}

func TestFilesOverHTTPSWithoutARootCertificateAreWarnedAbout(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)
	set(t, "PN_SMM_FILE_BASE_URL", "https://smm.example.com")

	var err error
	printed := printedBy(t, func() { err = configureFileProxy() })

	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(printed, "Files are served over HTTPS, but PN_SMM_FILE_ROOT_CA is not set") {
		t.Errorf("did not warn that games will refuse the certificate. Printed:\n%s", printed)
	}

	if globals.FileRootCA != nil {
		t.Error("a root certificate was set without one being given")
	}
}

// * A certificate that signed itself, in PEM form
func rootCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	return der, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestGamesAreHandedTheRootCertificateOfTheFileServer(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)

	der, encoded := rootCertificate(t)
	path := filepath.Join(t.TempDir(), "root.pem")

	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	set(t, "PN_SMM_FILE_BASE_URL", "https://smm.example.com", "PN_SMM_FILE_ROOT_CA", path)

	var err error
	printed := printedBy(t, func() { err = configureFileProxy() })

	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(globals.FileRootCA, der) {
		t.Error("games are not handed the certificate in DER form")
	}

	if strings.Contains(printed, "PN_SMM_FILE_ROOT_CA is not set") {
		t.Error("warned about a missing root certificate that was given")
	}
}

func TestARootCertificateThatCanNotBeUsedIsRefused(t *testing.T) {
	directory := t.TempDir()

	notACertificate := filepath.Join(directory, "key.pem")
	if err := os.WriteFile(notACertificate, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}}), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		notACertificate:                      "PN_SMM_FILE_ROOT_CA can not be used: the file holds a PRIVATE KEY, not a certificate",
		filepath.Join(directory, "none.pem"): "PN_SMM_FILE_ROOT_CA can not be used: open " + filepath.Join(directory, "none.pem") + ": no such file or directory",
	}

	for path, want := range cases {
		startClean(t)
		configuredAccountAPI(t)
		set(t, "PN_SMM_FILE_BASE_URL", "https://smm.example.com", "PN_SMM_FILE_ROOT_CA", path)

		wantError(t, configureFileProxy(), want)

		if globals.Presigner != nil {
			t.Error("games were given links to a server they will refuse")
		}
	}
}

func TestFilesCanAlsoBeServedWithoutTLSOnAPortOfTheirOwn(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)
	set(t, "PN_SMM_FILE_BASE_URL", "http://203.0.113.7:8080", "PN_SMM_FILE_HTTP_PORT", "8080")

	if err := configureFileProxy(); err != nil {
		t.Fatal(err)
	}

	if globals.AccountAPI.PlainFilesPort != 8080 {
		t.Errorf("files are served without TLS on port %d, want 8080", globals.AccountAPI.PlainFilesPort)
	}

	for _, port := range []int{1, 65535} {
		startClean(t)
		configuredAccountAPI(t)
		set(t, "PN_SMM_FILE_BASE_URL", "http://203.0.113.7:8080", "PN_SMM_FILE_HTTP_PORT", strconv.Itoa(port))

		if err := configureFileProxy(); err != nil || globals.AccountAPI.PlainFilesPort != port {
			t.Errorf("port %d was not taken: %v", port, err)
		}
	}

	for _, port := range []string{"0", "65536", "files"} {
		startClean(t)
		configuredAccountAPI(t)
		set(t, "PN_SMM_FILE_BASE_URL", "http://203.0.113.7:8080", "PN_SMM_FILE_HTTP_PORT", port)

		wantError(t, configureFileProxy(), "PN_SMM_FILE_HTTP_PORT is not a valid port. Expected 1-65535, got "+port)
	}
}

func TestTheJoinPageIsOnlyServedWhenTurnedOn(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)
	set(t, "PN_SMM_ACCOUNT_PUBLIC_URL", "https://smm.example.com")

	if err := configureJoinPage(); err != nil {
		t.Fatal(err)
	}

	if globals.AccountAPI.Join != nil {
		t.Error("the join page is served without PN_SMM_JOIN_PAGE or PN_SMM_INVITE_CODE")
	}
}

// * Turned on with no invite code, anyone may join, and the page does
// * not ask for one
func TestTheJoinPageCanBeOpenToAnyone(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)
	set(t, "PN_SMM_JOIN_PAGE", "1", "PN_SMM_ACCOUNT_PUBLIC_URL", "https://smm.example.com")

	if err := configureJoinPage(); err != nil {
		t.Fatal(err)
	}

	if globals.AccountAPI.Join == nil {
		t.Fatal("the join page is not served with PN_SMM_JOIN_PAGE=1")
	}

	if page := joinPage(t); strings.Contains(strings.ToLower(page), "invite") {
		t.Error("the page asks for an invite code when there is none")
	}
}

func TestTheJoinPageNeedsTheAccountAPI(t *testing.T) {
	startClean(t)
	set(t, "PN_SMM_INVITE_CODE", "mushroom", "PN_SMM_ACCOUNT_PUBLIC_URL", "https://smm.example.com")

	wantError(t, configureJoinPage(), "the join page is turned on, but it is served on the account API's port, which needs PN_SMM_ACCOUNT_HTTP_PORT")
}

func TestTheJoinPageNeedsTheAddressCemuReachesTheAccountAPIBy(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)
	set(t, "PN_SMM_INVITE_CODE", "mushroom")

	wantError(t, configureJoinPage(), "the join page is turned on, but PN_SMM_ACCOUNT_PUBLIC_URL is not set. It is how players' Cemu reaches the account API, such as https://smm.example.com")
}

func TestAConsolePluginThatCanNotBeReadIsRefused(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)

	missing := filepath.Join(t.TempDir(), "smm.wps")
	set(t, "PN_SMM_INVITE_CODE", "mushroom", "PN_SMM_ACCOUNT_PUBLIC_URL", "https://smm.example.com", "PN_SMM_CONSOLE_PLUGIN", missing)

	wantError(t, configureJoinPage(), "PN_SMM_CONSOLE_PLUGIN can not be read: open "+missing+": no such file or directory")
}

func joinPage(t *testing.T) string {
	t.Helper()

	response := httptest.NewRecorder()
	globals.AccountAPI.Join.ServeHTTP(response, httptest.NewRequest(http.MethodGet, globals.AccountAPI.JoinPath, nil))

	if response.Code != http.StatusOK {
		t.Fatalf("the join page answered %d", response.Code)
	}

	return response.Body.String()
}

func TestTheJoinPageOnlyOffersCemuWithoutTheConsolePlugin(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)
	set(t, "PN_SMM_INVITE_CODE", " mushroom ", "PN_SMM_ACCOUNT_PUBLIC_URL", "https://smm.example.com/")

	var err error
	printed := printedBy(t, func() { err = configureJoinPage() })

	if err != nil {
		t.Fatal(err)
	}

	if globals.AccountAPI.JoinPath != "/join" {
		t.Errorf("the join page is at %q, want /join", globals.AccountAPI.JoinPath)
	}

	if page := joinPage(t); strings.Contains(page, "/join/wiiu") {
		t.Error("a Wii U is offered without the console plugin")
	}

	// * The slash at the end of the address is left out
	if !strings.Contains(printed, "Players can join at https://smm.example.com/join") {
		t.Errorf("did not say where players join. Printed:\n%s", printed)
	}
}

func TestTheJoinPageOffersAWiiUWithTheConsolePlugin(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)

	plugin := filepath.Join(t.TempDir(), "smm.wps")
	if err := os.WriteFile(plugin, []byte("plugin"), 0o600); err != nil {
		t.Fatal(err)
	}

	set(t, "PN_SMM_INVITE_CODE", "mushroom", "PN_SMM_ACCOUNT_PUBLIC_URL", "https://smm.example.com", "PN_SMM_CONSOLE_PLUGIN", plugin)

	if err := configureJoinPage(); err != nil {
		t.Fatal(err)
	}

	if page := joinPage(t); !strings.Contains(page, "/join/wiiu") {
		t.Error("a Wii U is not offered with the console plugin")
	}
}

func TestTheImportServiceIsOnlyServedWithAToken(t *testing.T) {
	startClean(t)
	useStore(t)

	if err := configureImportService(); err != nil {
		t.Fatal(err)
	}

	if globals.ImportServer != nil {
		t.Error("the import service is served without PN_SMM_IMPORT_TOKEN")
	}
}

func TestTheImportServiceIsServedOnThisMachineByDefault(t *testing.T) {
	startClean(t)
	useStore(t)
	set(t, "PN_SMM_IMPORT_TOKEN", " the-token ")

	if err := configureImportService(); err != nil {
		t.Fatal(err)
	}

	server := globals.ImportServer
	if server == nil || server.Addr != "127.0.0.1:8384" {
		t.Fatalf("the import service is served at %v, want 127.0.0.1:8384", server)
	}

	if server.ReadHeaderTimeout != 10*time.Second || server.ReadTimeout != time.Minute || server.WriteTimeout != time.Minute {
		t.Error("the import service can be held open by a slow client")
	}

	// * The token has its spaces taken off, and anything without it is refused
	for token, want := range map[string]int{"": http.StatusUnauthorized, "the-token ": http.StatusUnauthorized, "the-token": http.StatusNotFound} {
		request := httptest.NewRequest(http.MethodGet, "/v1/import/courses/940000", nil)
		request.Header.Set("Authorization", "Bearer "+token)

		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, request)

		if response.Code != want {
			t.Errorf("with token %q the import service answered %d, want %d", token, response.Code, want)
		}
	}
}

func TestTheImportServiceIsOnlyServedOnALoopbackAddress(t *testing.T) {
	for address, allowed := range map[string]bool{
		"127.0.0.1:9000":    true,
		"[::1]:9000":        true,
		"0.0.0.0:8384":      false,
		"192.0.2.1:8384":    false,
		":8384":             false,
		"127.0.0.1":         false,
		"localhost:8384":    false,
		"smm.example.com:1": false,
	} {
		startClean(t)
		useStore(t)
		set(t, "PN_SMM_IMPORT_TOKEN", "the-token", "PN_SMM_IMPORT_ADDRESS", address)

		err := configureImportService()

		if allowed {
			if err != nil || globals.ImportServer == nil || globals.ImportServer.Addr != address {
				t.Errorf("%s: refused (%v), want it served there", address, err)
			}

			continue
		}

		wantError(t, err, "PN_SMM_IMPORT_ADDRESS must be a loopback address, such as 127.0.0.1:8384, as the import service is only for this machine. Got "+address)

		if globals.ImportServer != nil {
			t.Errorf("%s: the import service is served there", address)
		}
	}
}

// * An S3 server in memory, which the S3 settings point at, and which
// * the server's S3 client is made for as it is at start-up
func fakeS3(t *testing.T) *s3test.Server {
	t.Helper()

	server := s3test.Start(t)

	set(t,
		"PN_SMM_CONFIG_S3_ENDPOINT", server.Endpoint,
		"PN_SMM_CONFIG_S3_ACCESS_KEY", s3test.AccessKey,
		"PN_SMM_CONFIG_S3_ACCESS_SECRET", s3test.SecretKey,
		"PN_SMM_CONFIG_S3_SECURE", "false",
		"PN_SMM_CONFIG_S3_BUCKET", s3test.Bucket,
		"PN_SMM_CONFIG_S3_KEY_BASE", "courses",
	)

	client, _, err := globals.S3ClientFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	globals.MinIOClient = client

	return server
}

func TestTheEventCourseMetadataFileIsRequired(t *testing.T) {
	startClean(t)
	useStore(t)
	fakeS3(t)

	err := ensureEventCourseMetadataFileExists()
	if err == nil || !strings.HasPrefix(err.Error(), "Failed to stat event course metadata file. Ensure your S3 credentials are correct and the file is uploaded to your bucket as courses/900000.bin. S3 error: ") {
		t.Errorf("got %v, want the missing file named", err)
	}

	if _, nexError := globals.Store.GetObjectInfoByDataID(types.UInt64(store.EventCourseMetadataDataID)); nexError == nil {
		t.Error("the event course metadata object was made without its file")
	}
}

func TestTheEventCourseMetadataObjectIsKeptTheSizeOfItsFile(t *testing.T) {
	startClean(t)
	useStore(t)

	s3 := fakeS3(t)

	for _, size := range []int{12, 40} {
		s3.Put(t, "courses/900000.bin", bytes.Repeat([]byte{7}, size))

		if err := ensureEventCourseMetadataFileExists(); err != nil {
			t.Fatal(err)
		}

		info, nexError := globals.Store.GetObjectInfoByDataID(types.UInt64(store.EventCourseMetadataDataID))
		if nexError != nil {
			t.Fatalf("the event course metadata object is not there: %v", nexError)
		}

		if int(info.Size) != size || info.DataType != 50 {
			t.Errorf("the object has size %d and data type %d, want %d and 50", info.Size, info.DataType, size)
		}
	}
}

// * Runs configure in a process of its own, as it ends the process when
// * a setting is refused. The settings are those of the parent test
func TestConfigureInAProcessOfItsOwn(t *testing.T) {
	if os.Getenv("SMM_TEST_CONFIGURE") != "1" {
		t.Skip("only run by the start-up tests")
	}

	configure()

	if globals.AccountAPI == nil || globals.Presigner == nil || globals.ImportServer == nil {
		os.Exit(3)
	}

	globals.Logger.Info("start-up finished")
}

func startUp(t *testing.T) (string, int) {
	t.Helper()

	command := exec.Command(os.Args[0], "-test.run=^TestConfigureInAProcessOfItsOwn$", "-test.count=1")
	command.Env = append(os.Environ(), "SMM_TEST_CONFIGURE=1")

	// * Away from any .env file, and where the logger can write its files
	command.Dir = t.TempDir()

	printed, err := command.CombinedOutput()

	code := 0
	if exitError, ok := err.(*exec.ExitError); ok {
		code = exitError.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}

	return string(printed), code
}

func setEverything(t *testing.T) {
	t.Helper()

	fakeS3(t).Put(t, "courses/900000.bin", []byte{1})

	set(t,
		"PN_SMM_ACCOUNTS", "local",
		"PN_SMM_AUTHENTICATION_SERVER_PORT", "6000",
		"PN_SMM_SECURE_SERVER_HOST", "203.0.113.7",
		"PN_SMM_SECURE_SERVER_PORT", "6001",
		"PN_SMM_GRPC_SERVER_PORT", "50051",
		"PN_SMM_GRPC_API_KEY", "grpc-key",
		"PN_SMM_DATABASE", "sqlite",
		"PN_SMM_SQLITE_PATH", filepath.Join(t.TempDir(), "smm.db"),
		"PN_SMM_ACCOUNT_HTTP_PORT", "8443",
		"PN_SMM_FILE_BASE_URL", "http://203.0.113.7:8443",
		"PN_SMM_INVITE_CODE", "mushroom",
		"PN_SMM_ACCOUNT_PUBLIC_URL", "http://203.0.113.7:8443",
		"PN_SMM_IMPORT_TOKEN", "the-token",
	)
}

func TestTheServerStartsWithEverythingSet(t *testing.T) {
	startClean(t)
	setEverything(t)

	printed, code := startUp(t)

	if code != 0 {
		t.Fatalf("start-up ended with code %d. Printed:\n%s", code, printed)
	}

	for _, want := range []string{
		// * It is started away from any .env file
		"Error loading .env file",
		"Using accounts stored in this servers database",
		"Event course metadata object found and is up to date!",
		"Games upload and download files through this server, at http://203.0.113.7:8443/v1/files",
		"Players can join at http://203.0.113.7:8443/join",
		"start-up finished",
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("start-up did not print %q. Printed:\n%s", want, printed)
		}
	}

	if strings.Contains(printed, "Insecure gRPC server detected") || strings.Contains(printed, "PN_SMM_CONFIG_S3_SECURE environment variable not set") {
		t.Errorf("start-up warned about a setting that was given. Printed:\n%s", printed)
	}
}

func TestTheServerWarnsAboutAGRPCServerWithoutAKey(t *testing.T) {
	startClean(t)
	setEverything(t)
	set(t, "PN_SMM_GRPC_API_KEY", "")

	printed, code := startUp(t)

	if code != 0 || !strings.Contains(printed, "Insecure gRPC server detected. PN_SMM_GRPC_API_KEY environment variable not set") {
		t.Errorf("start-up ended with code %d and did not warn. Printed:\n%s", code, printed)
	}
}

func TestTheServerDoesNotStartWithASettingRefused(t *testing.T) {
	cases := map[string][]string{
		"PN_SMM_ACCOUNTS is not a supported account source. Expected grpc or local, got ldap":                                      {"PN_SMM_ACCOUNTS", "ldap"},
		"PN_SMM_AUTHENTICATION_SERVER_PORT environment variable not set":                                                           {"PN_SMM_AUTHENTICATION_SERVER_PORT", ""},
		"PN_SMM_ACCOUNT_GRPC_HOST environment variable not set":                                                                    {"PN_SMM_ACCOUNTS", "grpc"},
		"PN_SMM_DATABASE is not a supported database. Expected postgres or sqlite, got mysql":                                      {"PN_SMM_DATABASE", "mysql"},
		"PN_SMM_IMPORT_ADDRESS must be a loopback address, such as 127.0.0.1:8384, as the import service is only for this machine": {"PN_SMM_IMPORT_ADDRESS", "0.0.0.0:8384"},
	}

	for want, changed := range cases {
		t.Run(want, func(t *testing.T) {
			startClean(t)
			setEverything(t)
			set(t, changed...)

			printed, _ := startUp(t)

			if !strings.Contains(printed, want) {
				t.Errorf("start-up did not say %q. Printed:\n%s", want, printed)
			}

			if strings.Contains(printed, "start-up finished") {
				t.Error("the server was configured with a setting refused")
			}
		})
	}
}

func TestTheServerEndsWithAFailureCodeWhenASettingIsRefused(t *testing.T) {

	startClean(t)
	setEverything(t)
	set(t, "PN_SMM_AUTHENTICATION_SERVER_PORT", "")

	if _, code := startUp(t); code == 0 {
		t.Error("start-up was refused, but ended with exit code 0")
	}
}

func TestTheFileProxyLinksPointAtTheBaseAddress(t *testing.T) {
	startClean(t)
	configuredAccountAPI(t)
	set(t, "PN_SMM_FILE_BASE_URL", "https://smm.example.com", "PN_SMM_FILE_URL_SECRET", "secret")

	if err := configureFileProxy(); err != nil {
		t.Fatal(err)
	}

	link, err := globals.Presigner.GetObject("smm", "courses/940000.bin", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// * A game is sent the link as text
	sent, err := url.Parse(link.String())
	if err != nil {
		t.Fatal(err)
	}

	if sent.Scheme != "https" || sent.Host != "smm.example.com" || sent.Path != "/v1/files/courses/940000.bin" || sent.Query().Get("signature") == "" {
		t.Errorf("a game is sent to %s, want a signed link to https://smm.example.com/v1/files/courses/940000.bin", link)
	}
}
