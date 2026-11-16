package globals

import (
	pb "github.com/PretendoNetwork/grpc/go/account"
	"github.com/PretendoNetwork/nex-go/v2"
	datastorecommon "github.com/PretendoNetwork/nex-protocols-common-go/v2/datastore"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/minio/minio-go/v7"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var Logger *plogger.Logger
var KerberosPassword = "password" // * Default password
var AuthenticationServer *nex.PRUDPServer
var AuthenticationEndpoint *nex.PRUDPEndPoint
var SecureServer *nex.PRUDPServer
var SecureEndpoint *nex.PRUDPEndPoint
var Store store.Store

// LocalAccounts is true when accounts are kept in Store, instead of on an account server
var LocalAccounts bool
var DatastoreCommon *datastorecommon.CommonProtocol
var GRPCAccountClientConnection *grpc.ClientConn
var GRPCAccountClient pb.AccountClient
var GRPCAccountCommonMetadata metadata.MD
var MinIOClient *minio.Client
var Presigner *S3Presigner
var GRPCAPIKey string
var GRPCServerPort int
