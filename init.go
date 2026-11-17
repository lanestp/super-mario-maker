package main

import (
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	pb "github.com/PretendoNetwork/grpc/go/account"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/accountapi"
	"github.com/PretendoNetwork/super-mario-maker/fileproxy"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/open"
	"github.com/joho/godotenv"

	"github.com/PretendoNetwork/nex-go/v2"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func init() {
	globals.Logger = plogger.NewLogger()

	var err error

	err = godotenv.Load()
	if err != nil {
		globals.Logger.Warning("Error loading .env file")
	}

	authenticationServerPort := os.Getenv("PN_SMM_AUTHENTICATION_SERVER_PORT")
	secureServerHost := os.Getenv("PN_SMM_SECURE_SERVER_HOST")
	secureServerPort := os.Getenv("PN_SMM_SECURE_SERVER_PORT")
	gRPCServerPort := os.Getenv("PN_SMM_GRPC_SERVER_PORT")
	gRPCAPIKey := os.Getenv("PN_SMM_GRPC_API_KEY")
	accountGRPCHost := os.Getenv("PN_SMM_ACCOUNT_GRPC_HOST")
	accountGRPCPort := os.Getenv("PN_SMM_ACCOUNT_GRPC_PORT")
	accountGRPCAPIKey := os.Getenv("PN_SMM_ACCOUNT_GRPC_API_KEY")

	accountsMode := strings.ToLower(strings.TrimSpace(os.Getenv("PN_SMM_ACCOUNTS")))
	if accountsMode == "" {
		accountsMode = "grpc"
	}

	if accountsMode != "grpc" && accountsMode != "local" {
		globals.Logger.Errorf("PN_SMM_ACCOUNTS is not a supported account source. Expected grpc or local, got %s", accountsMode)
		os.Exit(1)
	}

	globals.LocalAccounts = accountsMode == "local"

	kerberosPassword := make([]byte, 0x10)
	_, err = rand.Read(kerberosPassword)
	if err != nil {
		globals.Logger.Error("Error generating Kerberos password")
		os.Exit(0)
	}

	globals.KerberosPassword = string(kerberosPassword)

	globals.AuthenticationServerAccount = nex.NewAccount(1, "Quazal Authentication", globals.KerberosPassword)
	globals.SecureServerAccount = nex.NewAccount(2, "Quazal Rendez-Vous", globals.KerberosPassword)

	if strings.TrimSpace(authenticationServerPort) == "" {
		globals.Logger.Error("PN_SMM_AUTHENTICATION_SERVER_PORT environment variable not set")
		os.Exit(0)
	}

	if port, err := strconv.Atoi(authenticationServerPort); err != nil {
		globals.Logger.Errorf("PN_SMM_AUTHENTICATION_SERVER_PORT is not a valid port. Expected 0-65535, got %s", authenticationServerPort)
		os.Exit(0)
	} else if port < 0 || port > 65535 {
		globals.Logger.Errorf("PN_SMM_AUTHENTICATION_SERVER_PORT is not a valid port. Expected 0-65535, got %s", authenticationServerPort)
		os.Exit(0)
	}

	if strings.TrimSpace(secureServerHost) == "" {
		globals.Logger.Error("PN_SMM_SECURE_SERVER_HOST environment variable not set")
		os.Exit(0)
	}

	if strings.TrimSpace(secureServerPort) == "" {
		globals.Logger.Error("PN_SMM_SECURE_SERVER_PORT environment variable not set")
		os.Exit(0)
	}

	if port, err := strconv.Atoi(secureServerPort); err != nil {
		globals.Logger.Errorf("PN_SMM_SECURE_SERVER_PORT is not a valid port. Expected 0-65535, got %s", secureServerPort)
		os.Exit(0)
	} else if port < 0 || port > 65535 {
		globals.Logger.Errorf("PN_SMM_SECURE_SERVER_PORT is not a valid port. Expected 0-65535, got %s", secureServerPort)
		os.Exit(0)
	}

	gRPCPort, err := strconv.Atoi(gRPCServerPort)
	if err != nil {
		globals.Logger.Errorf("PN_SMM_GRPC_SERVER_PORT is not a valid port. Expected 0-65535, got %s", gRPCServerPort)
		os.Exit(0)
	} else if gRPCPort < 0 || gRPCPort > 65535 {
		globals.Logger.Errorf("PN_SMM_GRPC_SERVER_PORT is not a valid port. Expected 0-65535, got %s", gRPCServerPort)
		os.Exit(0)
	}
	globals.GRPCServerPort = gRPCPort

	if strings.TrimSpace(gRPCAPIKey) == "" {
		globals.Logger.Warning("Insecure gRPC server detected. PN_SMM_GRPC_API_KEY environment variable not set")
	}
	globals.GRPCAPIKey = gRPCAPIKey

	if globals.LocalAccounts {
		globals.Logger.Info("Using accounts stored in this servers database. The account gRPC server is not used")
	} else {
		if strings.TrimSpace(accountGRPCHost) == "" {
			globals.Logger.Error("PN_SMM_ACCOUNT_GRPC_HOST environment variable not set")
			os.Exit(0)
		}

		if strings.TrimSpace(accountGRPCPort) == "" {
			globals.Logger.Error("PN_SMM_ACCOUNT_GRPC_PORT environment variable not set")
			os.Exit(0)
		}

		if port, err := strconv.Atoi(accountGRPCPort); err != nil {
			globals.Logger.Errorf("PN_SMM_ACCOUNT_GRPC_PORT is not a valid port. Expected 0-65535, got %s", accountGRPCPort)
			os.Exit(0)
		} else if port < 0 || port > 65535 {
			globals.Logger.Errorf("PN_SMM_ACCOUNT_GRPC_PORT is not a valid port. Expected 0-65535, got %s", accountGRPCPort)
			os.Exit(0)
		}

		if strings.TrimSpace(accountGRPCAPIKey) == "" {
			globals.Logger.Warning("Insecure gRPC server detected. PN_SMM_ACCOUNT_GRPC_API_KEY environment variable not set")
		}

		globals.GRPCAccountClientConnection, err = grpc.NewClient(fmt.Sprintf("%s:%s", accountGRPCHost, accountGRPCPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			globals.Logger.Criticalf("Failed to connect to account gRPC server: %v", err)
			os.Exit(0)
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

	ensureEventCourseMetadataFileExists()

	configureAccountAPI()
	configureFileProxy()
}

// * The account API is what a console asks where the game server
// * is. It is only served when accounts are kept in this servers
// * database and PN_SMM_ACCOUNT_HTTP_PORT is set
func configureAccountAPI() {
	accountHTTPPort := strings.TrimSpace(os.Getenv("PN_SMM_ACCOUNT_HTTP_PORT"))
	if accountHTTPPort == "" {
		return
	}

	if !globals.LocalAccounts {
		globals.Logger.Error("PN_SMM_ACCOUNT_HTTP_PORT is set, but the account API needs PN_SMM_ACCOUNTS to be local")
		os.Exit(1)
	}

	port, err := strconv.Atoi(accountHTTPPort)
	if err != nil || port < 1 || port > 65535 {
		globals.Logger.Errorf("PN_SMM_ACCOUNT_HTTP_PORT is not a valid port. Expected 1-65535, got %s", accountHTTPPort)
		os.Exit(1)
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
		globals.Logger.Errorf("The account API can not be served over TLS: %s", err.Error())
		os.Exit(1)
	}

	if err := server.Config.Validate(); err != nil {
		globals.Logger.Errorf("The account API can not be served: %s. The game server host comes from PN_SMM_SECURE_SERVER_HOST", err.Error())
		os.Exit(1)
	}

	globals.AccountAPI = server
}

// * Has games upload and download course files through this server, and
// * not from S3 directly, when PN_SMM_FILE_BASE_URL says how games reach
// * this server. The files are served on the account API's port
func configureFileProxy() {
	baseURL := strings.TrimSpace(os.Getenv("PN_SMM_FILE_BASE_URL"))
	if baseURL == "" {
		return
	}

	if globals.AccountAPI == nil {
		globals.Logger.Error("PN_SMM_FILE_BASE_URL is set, but files are served on the account API's port, which needs PN_SMM_ACCOUNT_HTTP_PORT")
		os.Exit(1)
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		globals.Logger.Errorf("PN_SMM_FILE_BASE_URL is not a valid address. Expected something like https://smm.example.com, got %s", baseURL)
		os.Exit(1)
	}

	// * Links signed before a restart stop working when the secret is
	// * made new each time, which is fine as they only last 15 minutes
	secret := []byte(os.Getenv("PN_SMM_FILE_URL_SECRET"))
	if len(secret) == 0 {
		secret = make([]byte, 32)

		if _, err := rand.Read(secret); err != nil {
			globals.Logger.Error("Error generating the secret that signs links to files")
			os.Exit(1)
		}
	}

	proxy := &fileproxy.Proxy{
		BaseURL:  parsed,
		Secret:   secret,
		Storage:  fileproxy.S3{Client: globals.MinIOClient, Bucket: os.Getenv("PN_SMM_CONFIG_S3_BUCKET")},
		LogError: func(err error) { globals.Logger.Error(err.Error()) },
	}

	globals.Presigner = proxy
	globals.AccountAPI.Files = proxy.Handler()
	globals.AccountAPI.FilesPath = fileproxy.Path

	globals.Logger.Infof("Games upload and download files through this server, at %s", parsed.JoinPath(fileproxy.Path))
}

func ensureEventCourseMetadataFileExists() {
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
		globals.Logger.Errorf("Failed to stat event course metadata file. Ensure your S3 credentials are correct and the file is uploaded to your bucket as %s. S3 error: %s", key, err.Error())
		os.Exit(1)
	}

	globals.Logger.Success("Event course metadata file found. Verifying database")

	err = globals.Store.EnsureEventCourseMetadata(objectSizeS3)
	if err != nil {
		globals.Logger.Errorf("Error creating event course metadata object: %s", err.Error())
		os.Exit(1)
	}

	globals.Logger.Success("Event course metadata object found and is up to date!")
}
