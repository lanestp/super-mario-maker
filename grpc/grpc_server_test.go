package grpc

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	pb "github.com/PretendoNetwork/grpc/go/smm/v1"
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// * The server as StartGRPCServer makes it, reached in memory, with the
// * API key given and a store holding one course
func newServer(t *testing.T, apiKey string) (pb.SMMServiceClient, store.Store, uint64) {
	t.Helper()

	globals.Logger = plogger.NewLogger()

	opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
	if err != nil {
		t.Fatal(err)
	}

	previousStore, previousKey := globals.Store, globals.GRPCAPIKey
	globals.Store, globals.GRPCAPIKey = opened, apiKey

	param := datastore_types.NewDataStorePreparePostParam()
	param.Name = types.NewString("My Course")
	param.DataType = 2

	dataID, nexError := opened.InitializeObjectByPreparePostParam(types.PID(1800000000), param)
	if nexError != nil {
		t.Fatal(nexError)
	}

	if nexError := opened.UpdateObjectUploadCompletedByDataID(types.UInt64(dataID), true); nexError != nil {
		t.Fatal(nexError)
	}

	listener := bufconn.Listen(1 << 20)

	server := grpc.NewServer(grpc.UnaryInterceptor(apiKeyInterceptor))
	pb.RegisterSMMServiceServer(server, &gRPCSMMServer{})

	go server.Serve(listener)

	connection, err := grpc.NewClient("passthrough:///in-memory",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		connection.Close()
		server.Stop()
		opened.Close()
		globals.Store, globals.GRPCAPIKey = previousStore, previousKey
	})

	return pb.NewSMMServiceClient(connection), opened, dataID
}

func withKey(key string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "X-API-Key", key)
}

func TestDeletingACourseOverGRPCSaysWhichCourseWasDeleted(t *testing.T) {
	client, opened, dataID := newServer(t, "the-key")

	response, err := client.DeleteCourse(withKey("the-key"), &pb.DeleteCourseRequest{DataId: dataID})
	if err != nil {
		t.Fatal(err)
	}

	if !response.Success || response.CourseName != "My Course" {
		t.Errorf("got %+v, want My Course deleted", response)
	}

	if opened.IsObjectAvailable(types.UInt64(dataID)) == nil {
		t.Error("the course is still available")
	}
}

func TestDeletingACourseThatIsNotThereIsNotFound(t *testing.T) {
	client, _, dataID := newServer(t, "the-key")

	for _, missing := range []uint64{dataID + 1, dataID} {
		if missing == dataID {
			// * A course that was deleted is not there any more
			if _, err := client.DeleteCourse(withKey("the-key"), &pb.DeleteCourseRequest{DataId: dataID}); err != nil {
				t.Fatal(err)
			}
		}

		_, err := client.DeleteCourse(withKey("the-key"), &pb.DeleteCourseRequest{DataId: missing})
		if status.Code(err) != codes.NotFound || status.Convert(err).Message() != "Course not found" {
			t.Errorf("course %d: got %v, want NotFound", missing, err)
		}
	}
}

func TestTheGRPCServerRefusesARequestWithoutTheAPIKey(t *testing.T) {
	client, opened, dataID := newServer(t, "the-key")

	for name, ctx := range map[string]context.Context{
		"no key":        context.Background(),
		"the wrong key": withKey("another-key"),
		"an empty key":  withKey(""),
	} {
		_, err := client.DeleteCourse(ctx, &pb.DeleteCourseRequest{DataId: dataID})
		if err == nil || status.Convert(err).Message() != "Missing or invalid API key" {
			t.Errorf("%s: got %v, want the request refused", name, err)
		}
	}

	if opened.IsObjectAvailable(types.UInt64(dataID)) != nil {
		t.Error("a refused request deleted the course")
	}
}

// * PN_SMM_GRPC_API_KEY not being set is warned about as insecure, but
// * only a request that sends an empty key gets through
func TestWithoutAnAPIKeyOnlyARequestWithAnEmptyKeyIsTaken(t *testing.T) {
	client, _, dataID := newServer(t, "")

	if _, err := client.DeleteCourse(context.Background(), &pb.DeleteCourseRequest{DataId: dataID}); err == nil {
		t.Error("a request without a key was taken")
	}

	if _, err := client.DeleteCourse(withKey(""), &pb.DeleteCourseRequest{DataId: dataID}); err != nil {
		t.Errorf("a request with an empty key was refused: %v", err)
	}
}

// * A store whose courses are all available, but which fails to read or
// * delete them
type failingStore struct {
	store.Store
	failRead bool
}

func (failingStore) IsObjectAvailable(types.UInt64) *nex.Error {
	return nil
}

func (f failingStore) GetObjectInfoByDataID(types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error) {
	if f.failRead {
		return datastore_types.DataStoreMetaInfo{}, nex.NewError(nex.ResultCodes.DataStore.SystemFileError, "the database is gone")
	}

	return datastore_types.NewDataStoreMetaInfo(), nil
}

func (failingStore) DeleteObjectByDataID(types.UInt64) *nex.Error {
	return nex.NewError(nex.ResultCodes.DataStore.SystemFileError, "the database is gone")
}

func TestAStoreThatFailsIsAnInternalErrorWithoutItsDetails(t *testing.T) {
	for _, failRead := range []bool{true, false} {
		client, _, dataID := newServer(t, "the-key")
		globals.Store = failingStore{failRead: failRead}

		response, err := client.DeleteCourse(withKey("the-key"), &pb.DeleteCourseRequest{DataId: dataID})

		if status.Code(err) != codes.Internal || status.Convert(err).Message() != "internal server error" || response != nil {
			t.Errorf("failing to read %v: got %+v and %v, want an internal error", failRead, response, err)
		}
	}
}

func TestTheAPIKeyIsOnlyCheckedOnRequestsThatCarryMetadata(t *testing.T) {
	globals.GRPCAPIKey = "the-key"
	t.Cleanup(func() { globals.GRPCAPIKey = "" })

	called := 0
	handler := func(context.Context, any) (any, error) {
		called++
		return "answer", nil
	}

	incoming := func(pairs ...string) context.Context {
		return metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
	}

	if answer, err := apiKeyInterceptor(incoming("x-api-key", "the-key"), nil, &grpc.UnaryServerInfo{}, handler); err != nil || answer != "answer" {
		t.Errorf("the right key gave %v, %v", answer, err)
	}

	if _, err := apiKeyInterceptor(incoming("x-api-key", "wrong"), nil, &grpc.UnaryServerInfo{}, handler); err == nil {
		t.Error("the wrong key was taken")
	}

	// * A request from the network always has metadata, so this is only
	// * reached by calling the server in the same process
	if _, err := apiKeyInterceptor(context.Background(), nil, &grpc.UnaryServerInfo{}, handler); err != nil {
		t.Errorf("a call without metadata gave %v", err)
	}

	if called != 2 {
		t.Errorf("the handler was called %d times, want 2", called)
	}
}
