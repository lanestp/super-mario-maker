package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pb "github.com/PretendoNetwork/grpc/go/account"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/accountapi"
	"github.com/PretendoNetwork/super-mario-maker/archivefetch"
	"github.com/PretendoNetwork/super-mario-maker/fileproxy"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/importsvc"
	"github.com/PretendoNetwork/super-mario-maker/joinpage"
	"github.com/PretendoNetwork/super-mario-maker/seed"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/open"
	"github.com/joho/godotenv"

	"github.com/PretendoNetwork/nex-go/v2"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// * Reads the settings and gets everything ready to serve. It is run by
// * main, and not as init, so that the tests of this package can start
func configure() {
	globals.Logger = plogger.NewLogger()

	var err error

	err = godotenv.Load()
	if err != nil {
		globals.Logger.Warning("Error loading .env file")
	}

	gRPCAPIKey := os.Getenv("PN_SMM_GRPC_API_KEY")
	accountGRPCHost := os.Getenv("PN_SMM_ACCOUNT_GRPC_HOST")
	accountGRPCPort := os.Getenv("PN_SMM_ACCOUNT_GRPC_PORT")
	accountGRPCAPIKey := os.Getenv("PN_SMM_ACCOUNT_GRPC_API_KEY")

	globals.LocalAccounts, err = localAccountsFromEnv()
	if err != nil {
		globals.Logger.Error(err.Error())
		os.Exit(1)
	}

	kerberosPassword := make([]byte, 0x10)
	_, err = rand.Read(kerberosPassword)
	if err != nil {
		globals.Logger.Error("Error generating Kerberos password")
		os.Exit(1)
	}

	globals.KerberosPassword = string(kerberosPassword)

	globals.AuthenticationServerAccount = nex.NewAccount(1, "Quazal Authentication", globals.KerberosPassword)
	globals.SecureServerAccount = nex.NewAccount(2, "Quazal Rendez-Vous", globals.KerberosPassword)

	globals.GRPCServerPort, err = gameServerPortsFromEnv()
	if err != nil {
		globals.Logger.Error(err.Error())
		os.Exit(1)
	}

	if strings.TrimSpace(gRPCAPIKey) == "" {
		globals.Logger.Warning("Insecure gRPC server detected. PN_SMM_GRPC_API_KEY environment variable not set")
	}
	globals.GRPCAPIKey = gRPCAPIKey

	if globals.LocalAccounts {
		globals.Logger.Info("Using accounts stored in this servers database. The account gRPC server is not used")
	} else {
		if err := checkAccountServerFromEnv(); err != nil {
			globals.Logger.Error(err.Error())
			os.Exit(1)
		}

		if strings.TrimSpace(accountGRPCAPIKey) == "" {
			globals.Logger.Warning("Insecure gRPC server detected. PN_SMM_ACCOUNT_GRPC_API_KEY environment variable not set")
		}

		globals.GRPCAccountClientConnection, err = grpc.NewClient(fmt.Sprintf("%s:%s", accountGRPCHost, accountGRPCPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			globals.Logger.Criticalf("Failed to connect to account gRPC server: %v", err)
			os.Exit(1)
		}

		globals.GRPCAccountClient = pb.NewAccountClient(globals.GRPCAccountClientConnection)
		globals.GRPCAccountCommonMetadata = metadata.Pairs(
			"X-API-Key", accountGRPCAPIKey,
		)
	}

	minIOClient, s3SecureWasSet, err := globals.S3ClientFromEnv()
	if err != nil {
		panic(err)
	}

	if !s3SecureWasSet {
		globals.Logger.Warningf("PN_SMM_CONFIG_S3_SECURE environment variable not set. Using default value: %t", true)
	}

	globals.MinIOClient = minIOClient
	globals.Presigner = globals.NewS3Presigner(globals.MinIOClient)

	// * Connect to and setup the database
	globals.Store, err = open.FromEnv()
	if err != nil {
		globals.Logger.Critical(err.Error())
		os.Exit(1)
	}

	steps := []func() error{
		ensureEventCourseMetadataFileExists,
		configureAccountAPI,
		configureFileProxy,
		configureJoinPage,
		configureImportService,
		configureArchiveFetch,
	}

	for _, step := range steps {
		if err := step(); err != nil {
			globals.Logger.Error(err.Error())
			os.Exit(1)
		}
	}
}

// * Whether accounts are kept in this servers database (PN_SMM_ACCOUNTS
// * is local), or on an account gRPC server (grpc, or not set)
func localAccountsFromEnv() (bool, error) {
	accountsMode := strings.ToLower(strings.TrimSpace(os.Getenv("PN_SMM_ACCOUNTS")))
	if accountsMode == "" {
		accountsMode = "grpc"
	}

	if accountsMode != "grpc" && accountsMode != "local" {
		return false, fmt.Errorf("PN_SMM_ACCOUNTS is not a supported account source. Expected grpc or local, got %s", accountsMode)
	}

	return accountsMode == "local", nil
}

// * Checks that the port an environment variable gives is 0-65535
func checkPort(name, value string) error {
	if port, err := strconv.Atoi(value); err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("%s is not a valid port. Expected 0-65535, got %s", name, value)
	}

	return nil
}

// * Checks where the game servers are, and returns the gRPC server's port
func gameServerPortsFromEnv() (int, error) {
	authenticationServerPort := os.Getenv("PN_SMM_AUTHENTICATION_SERVER_PORT")
	secureServerHost := os.Getenv("PN_SMM_SECURE_SERVER_HOST")
	secureServerPort := os.Getenv("PN_SMM_SECURE_SERVER_PORT")
	gRPCServerPort := os.Getenv("PN_SMM_GRPC_SERVER_PORT")

	if strings.TrimSpace(authenticationServerPort) == "" {
		return 0, errors.New("PN_SMM_AUTHENTICATION_SERVER_PORT environment variable not set")
	}

	if err := checkPort("PN_SMM_AUTHENTICATION_SERVER_PORT", authenticationServerPort); err != nil {
		return 0, err
	}

	if strings.TrimSpace(secureServerHost) == "" {
		return 0, errors.New("PN_SMM_SECURE_SERVER_HOST environment variable not set")
	}

	if strings.TrimSpace(secureServerPort) == "" {
		return 0, errors.New("PN_SMM_SECURE_SERVER_PORT environment variable not set")
	}

	if err := checkPort("PN_SMM_SECURE_SERVER_PORT", secureServerPort); err != nil {
		return 0, err
	}

	if err := checkPort("PN_SMM_GRPC_SERVER_PORT", gRPCServerPort); err != nil {
		return 0, err
	}

	gRPCPort, _ := strconv.Atoi(gRPCServerPort)

	return gRPCPort, nil
}

// * Checks where the account gRPC server is, when accounts are kept there
func checkAccountServerFromEnv() error {
	accountGRPCHost := os.Getenv("PN_SMM_ACCOUNT_GRPC_HOST")
	accountGRPCPort := os.Getenv("PN_SMM_ACCOUNT_GRPC_PORT")

	if strings.TrimSpace(accountGRPCHost) == "" {
		return errors.New("PN_SMM_ACCOUNT_GRPC_HOST environment variable not set")
	}

	if strings.TrimSpace(accountGRPCPort) == "" {
		return errors.New("PN_SMM_ACCOUNT_GRPC_PORT environment variable not set")
	}

	return checkPort("PN_SMM_ACCOUNT_GRPC_PORT", accountGRPCPort)
}

// * The account API is what a console asks where the game server
// * is. It is only served when accounts are kept in this servers
// * database and PN_SMM_ACCOUNT_HTTP_PORT is set
func configureAccountAPI() error {
	accountHTTPPort := strings.TrimSpace(os.Getenv("PN_SMM_ACCOUNT_HTTP_PORT"))
	if accountHTTPPort == "" {
		return nil
	}

	if !globals.LocalAccounts {
		return errors.New("PN_SMM_ACCOUNT_HTTP_PORT is set, but the account API needs PN_SMM_ACCOUNTS to be local")
	}

	port, err := strconv.Atoi(accountHTTPPort)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("PN_SMM_ACCOUNT_HTTP_PORT is not a valid port. Expected 1-65535, got %s", accountHTTPPort)
	}

	// * The IDs Super Mario Maker asks for, one for each region
	gameServerIDs := []string{"1018DB00", "1018DC00", "1018DD00"}
	if configured := strings.TrimSpace(os.Getenv("PN_SMM_GAME_SERVER_IDS")); configured != "" {
		gameServerIDs = strings.Split(configured, ",")
		for i := range gameServerIDs {
			gameServerIDs[i] = strings.TrimSpace(gameServerIDs[i])
		}
	}

	gameServerPort, _ := strconv.Atoi(os.Getenv("PN_SMM_AUTHENTICATION_SERVER_PORT"))

	server := &accountapi.Server{
		Config: accountapi.Config{
			Accounts:       globals.Store,
			GameServerHost: strings.TrimSpace(os.Getenv("PN_SMM_SECURE_SERVER_HOST")),
			GameServerPort: gameServerPort,
			GameServerIDs:  gameServerIDs,
			LogError:       func(err error) { globals.Logger.Error(err.Error()) },
		},
		Port:               port,
		TLSCertificatePath: strings.TrimSpace(os.Getenv("PN_SMM_ACCOUNT_HTTP_TLS_CERT")),
		TLSKeyPath:         strings.TrimSpace(os.Getenv("PN_SMM_ACCOUNT_HTTP_TLS_KEY")),
		TLSDomain:          strings.TrimSpace(os.Getenv("PN_SMM_ACCOUNT_HTTP_TLS_DOMAIN")),
		TLSCacheDirectory:  strings.TrimSpace(os.Getenv("PN_SMM_ACCOUNT_HTTP_TLS_CACHE")),
	}

	// * Anything but a clear yes leaves old TLS refused
	server.LegacyTLS, _ = strconv.ParseBool(strings.TrimSpace(os.Getenv("PN_SMM_ACCOUNT_HTTP_TLS_LEGACY")))

	// * Found now, and not when the first console connects
	if _, err := server.TLSConfig(); err != nil {
		return fmt.Errorf("The account API can not be served over TLS: %s", err.Error())
	}

	if err := server.Config.Validate(); err != nil {
		return fmt.Errorf("The account API can not be served: %s. The game server host comes from PN_SMM_SECURE_SERVER_HOST", err.Error())
	}

	globals.AccountAPI = server

	return nil
}

// * Has games upload and download course files through this server, and
// * not from S3 directly, when PN_SMM_FILE_BASE_URL says how games reach
// * this server. The files are served on the account API's port
func configureFileProxy() error {
	baseURL := strings.TrimSpace(os.Getenv("PN_SMM_FILE_BASE_URL"))
	if baseURL == "" {
		return nil
	}

	if globals.AccountAPI == nil {
		return errors.New("PN_SMM_FILE_BASE_URL is set, but files are served on the account API's port, which needs PN_SMM_ACCOUNT_HTTP_PORT")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("PN_SMM_FILE_BASE_URL is not a valid address. Expected something like https://smm.example.com, got %s", baseURL)
	}

	// * Links signed before a restart stop working when the secret is
	// * made new each time, which is fine as they only last 15 minutes
	secret := []byte(os.Getenv("PN_SMM_FILE_URL_SECRET"))
	if len(secret) == 0 {
		secret = make([]byte, 32)

		if _, err := rand.Read(secret); err != nil {
			return errors.New("Error generating the secret that signs links to files")
		}
	}

	proxy := &fileproxy.Proxy{
		BaseURL:  parsed,
		Secret:   secret,
		Storage:  fileproxy.S3{Client: globals.MinIOClient, Bucket: os.Getenv("PN_SMM_CONFIG_S3_BUCKET")},
		LogError: func(err error) { globals.Logger.Error(err.Error()) },
	}

	// * A game checks the server it is sent to for files against the
	// * certificate it is handed, and nothing else
	if rootPath := strings.TrimSpace(os.Getenv("PN_SMM_FILE_ROOT_CA")); rootPath != "" {
		data, err := os.ReadFile(rootPath)
		if err == nil {
			globals.FileRootCA, err = globals.ParseRootCA(data)
		}

		if err != nil {
			return fmt.Errorf("PN_SMM_FILE_ROOT_CA can not be used: %s", err.Error())
		}
	} else if parsed.Scheme == "https" {
		globals.Logger.Warning("Files are served over HTTPS, but PN_SMM_FILE_ROOT_CA is not set. A game will refuse the server's certificate, as it has not been told which root to trust")
	}

	globals.Presigner = proxy
	globals.AccountAPI.Files = proxy.Handler()
	globals.AccountAPI.FilesPath = fileproxy.Path

	if plainPort := strings.TrimSpace(os.Getenv("PN_SMM_FILE_HTTP_PORT")); plainPort != "" {
		port, err := strconv.Atoi(plainPort)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("PN_SMM_FILE_HTTP_PORT is not a valid port. Expected 1-65535, got %s", plainPort)
		}

		globals.AccountAPI.PlainFilesPort = port
	}

	globals.Logger.Infof("Games upload and download files through this server, at %s", parsed.JoinPath(fileproxy.Path))

	return nil
}

func ensureEventCourseMetadataFileExists() error {
	// * The event course metadata file is REQUIRED for
	// * Super Mario Maker to open Course World. This
	// * ensures that it exists both in S3 and in the
	// * database before booting. It has the reserved
	// * DataID 900000
	bucket := os.Getenv("PN_SMM_CONFIG_S3_BUCKET")

	// * The same key the game is sent to when it downloads the file
	key := fmt.Sprintf("%s/%d.bin", os.Getenv("PN_SMM_CONFIG_S3_KEY_BASE"), store.EventCourseMetadataDataID)

	objectSizeS3, err := globals.S3ObjectSize(bucket, key)
	if err != nil {
		return fmt.Errorf("Failed to stat event course metadata file. Ensure your S3 credentials are correct and the file is uploaded to your bucket as %s. S3 error: %s", key, err.Error())
	}

	globals.Logger.Success("Event course metadata file found. Verifying database")

	err = globals.Store.EnsureEventCourseMetadata(objectSizeS3)
	if err != nil {
		return fmt.Errorf("Error creating event course metadata object: %s", err.Error())
	}

	globals.Logger.Success("Event course metadata object found and is up to date!")

	return nil
}

// * Serves the page where players make an account, on the account API's
// * port, when PN_SMM_INVITE_CODE says what they have to give to join
func configureJoinPage() error {
	inviteCode := strings.TrimSpace(os.Getenv("PN_SMM_INVITE_CODE"))
	if inviteCode == "" {
		return nil
	}

	if globals.AccountAPI == nil {
		return errors.New("PN_SMM_INVITE_CODE is set, but the join page is served on the account API's port, which needs PN_SMM_ACCOUNT_HTTP_PORT")
	}

	accountServerURL := strings.TrimSpace(os.Getenv("PN_SMM_ACCOUNT_PUBLIC_URL"))
	if accountServerURL == "" {
		return errors.New("PN_SMM_INVITE_CODE is set, but PN_SMM_ACCOUNT_PUBLIC_URL is not. It is how players' Cemu reaches the account API, such as https://smm.example.com")
	}

	// * Without the plugin, only Cemu is offered
	var plugin []byte

	if pluginPath := strings.TrimSpace(os.Getenv("PN_SMM_CONSOLE_PLUGIN")); pluginPath != "" {
		data, err := os.ReadFile(pluginPath)
		if err != nil {
			return fmt.Errorf("PN_SMM_CONSOLE_PLUGIN can not be read: %s", err.Error())
		}

		plugin = data
	}

	handler, err := joinpage.Handler(joinpage.Config{
		Accounts:         globals.Store,
		InviteCode:       inviteCode,
		AccountServerURL: strings.TrimRight(accountServerURL, "/"),
		GameServerHost:   strings.TrimSpace(os.Getenv("PN_SMM_SECURE_SERVER_HOST")),
		GameServerPort:   globals.AccountAPI.Config.GameServerPort,
		Plugin:           plugin,
		LogError:         func(err error) { globals.Logger.Error(err.Error()) },
	})
	if err != nil {
		return err
	}

	globals.AccountAPI.Join = handler
	globals.AccountAPI.JoinPath = joinpage.Path

	globals.Logger.Infof("Players can join at %s%s", strings.TrimRight(accountServerURL, "/"), joinpage.Path)

	return nil
}

// * Serves the import service, which tools on this machine send archived
// * courses to, when PN_SMM_IMPORT_TOKEN is set. It is only ever served
// * on a loopback address
func configureImportService() error {
	token := strings.TrimSpace(os.Getenv("PN_SMM_IMPORT_TOKEN"))
	if token == "" {
		return nil
	}

	address := strings.TrimSpace(os.Getenv("PN_SMM_IMPORT_ADDRESS"))
	if address == "" {
		address = "127.0.0.1:8384"
	}

	host, _, err := net.SplitHostPort(address)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("PN_SMM_IMPORT_ADDRESS must be a loopback address, such as 127.0.0.1:8384, as the import service is only for this machine. Got %s", address)
	}

	handler, err := importsvc.Handler(importsvc.Config{
		Token:    token,
		Seeder:   newSeeder(),
		LogError: func(err error) { globals.Logger.Error(err.Error()) },
	})
	if err != nil {
		return err
	}

	globals.ImportServer = &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
		WriteTimeout:      time.Minute,
	}

	return nil
}

// * A seeder that writes course files where the server hands them out from
func newSeeder() *seed.Seeder {
	storage := fileproxy.S3{Client: globals.MinIOClient, Bucket: os.Getenv("PN_SMM_CONFIG_S3_BUCKET")}

	return &seed.Seeder{
		Store:   globals.Store,
		KeyBase: os.Getenv("PN_SMM_CONFIG_S3_KEY_BASE"),
		PutFile: func(key string, data []byte) error {
			return storage.Put(context.Background(), key, data)
		},
	}
}

// * Turns on fetching courses from the archive while the server runs,
// * when PN_SMM_ARCHIVE_FETCH is 1: a course a player asks for by its ID
// * that is not here is fetched then, and PN_SMM_ARCHIVE_FILL_LIMIT (0,
// * the default, for none) is how many random courses from the whole
// * archive are brought in in the background. PN_SMM_ARCHIVE_WAIT is the
// * least time between the background's requests to the archive
func configureArchiveFetch() error {
	if strings.TrimSpace(os.Getenv("PN_SMM_ARCHIVE_FETCH")) != "1" {
		return nil
	}

	fillLimit := int64(0)

	if value := strings.TrimSpace(os.Getenv("PN_SMM_ARCHIVE_FILL_LIMIT")); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			return fmt.Errorf("PN_SMM_ARCHIVE_FILL_LIMIT must be a number of courses, 0 or more. Got %q", value)
		}

		fillLimit = parsed
	}

	fillGap := 3 * time.Second

	if value := strings.TrimSpace(os.Getenv("PN_SMM_ARCHIVE_WAIT")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed < time.Second {
			return fmt.Errorf("PN_SMM_ARCHIVE_WAIT must be a duration of at least 1s, such as 3s. Got %q", value)
		}

		fillGap = parsed
	}

	catalog, err := archiveCatalogFromEnv()
	if err != nil {
		return err
	}

	fetcher, err := archivefetch.New(archivefetch.Config{
		Seeder:    newSeeder(),
		Catalog:   catalog,
		FillLimit: fillLimit,
		FillGap:   fillGap,
		UrgentGap: time.Second,
		Log:       func(line string) { globals.Logger.Info(line) },
	})
	if err != nil {
		return err
	}

	globals.FetchFromArchive = fetcher.Fetch

	if catalog != nil {
		globals.ArchiveNewArrivals = fetcher.NewArrivals
		globals.ArchiveArrivals = fetcher.Arrivals
	}

	if fillLimit > 0 {
		globals.ArchiveFill = fetcher.Fill
	}

	globals.Logger.Infof("Courses players ask for that are not here are fetched from the archive. The background fill brings in up to %d more", fillLimit)

	return nil
}

// * The archive's course database (smm_data.db from
// * https://archive.org/details/mario_maker_database), when there is one:
// * at PN_SMM_ARCHIVE_DB, or else at archive/smm_data.db next to the
// * server's own SQLite database. nil when there is none
func archiveCatalogFromEnv() (*archivefetch.Catalog, error) {
	path := strings.TrimSpace(os.Getenv("PN_SMM_ARCHIVE_DB"))
	if path == "" {
		database := strings.TrimSpace(os.Getenv("PN_SMM_SQLITE_PATH"))
		if database == "" {
			return nil, nil
		}

		path = filepath.Join(filepath.Dir(database), "archive", "smm_data.db")

		if _, err := os.Stat(path); err != nil {
			return nil, nil
		}
	}

	catalog, err := archivefetch.OpenCatalog(path)
	if err != nil {
		return nil, fmt.Errorf("PN_SMM_ARCHIVE_DB: %w", err)
	}

	globals.Logger.Infof("Courses are described from the archive's course database at %s, and the fill brings them in most played first", path)

	return catalog, nil
}
