package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"strings"

	pb "github.com/PretendoNetwork/grpc/go/account"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store/open"
	"github.com/joho/godotenv"

	"github.com/PretendoNetwork/nex-go/v2"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
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

	s3Endpoint := os.Getenv("PN_SMM_CONFIG_S3_ENDPOINT")
	s3AccessKey := os.Getenv("PN_SMM_CONFIG_S3_ACCESS_KEY")
	s3AccessSecret := os.Getenv("PN_SMM_CONFIG_S3_ACCESS_SECRET")
	s3SecureEnv := os.Getenv("PN_SMM_CONFIG_S3_SECURE")

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

	staticCredentials := credentials.NewStaticV4(s3AccessKey, s3AccessSecret, "")

	s3Secure, err := strconv.ParseBool(s3SecureEnv)
	if err != nil {
		globals.Logger.Warningf("PN_SMM_CONFIG_S3_SECURE environment variable not set. Using default value: %t", true)
		s3Secure = true
	}

	minIOClient, err := minio.New(s3Endpoint, &minio.Options{
		Creds:  staticCredentials,
		Secure: s3Secure,
	})
	if err != nil {
		panic(err)
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
}

func ensureEventCourseMetadataFileExists() {
	// * The event course metadata file is REQUIRED for
	// * Super Mario Maker to open Course World. This
	// * ensures that it exists both in S3 and in the
	// * database before booting. It has the reserved
	// * DataID 900000
	bucket := os.Getenv("PN_SMM_CONFIG_S3_BUCKET")
	key := "900000.bin"

	objectSizeS3, err := globals.S3ObjectSize(bucket, key)
	if err != nil {
		globals.Logger.Errorf("Failed to stat event course metadata file. Ensure your S3 credentials are correct and the 900000.bin file is uploaded to your bucket. S3 error: %s", err.Error())
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
