package nex_datastore_super_mario_maker

import (
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastorecommon "github.com/PretendoNetwork/nex-protocols-common-go/v2/datastore"
	datastore_super_mario_maker "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker"
	datastore_super_mario_maker_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// * The bucket and key base the handlers read from the environment
const (
	testBucket  = "smm"
	testKeyBase = "courses"
)

// * The certificate games are told to check the file server against
var testRootCA = []byte("test root certificate")

// * A game's connection. Handlers only ask it who is calling
type fakeConnection struct {
	pid types.PID
}

func (c *fakeConnection) Endpoint() nex.EndpointInterface { return nil }
func (c *fakeConnection) Address() net.Addr               { return nil }
func (c *fakeConnection) PID() types.PID                  { return c.pid }
func (c *fakeConnection) SetPID(pid types.PID)            { c.pid = pid }

// * A request from a game. Handlers are given its parameters already
// * read, so all they use is its sender
type fakePacket struct {
	sender  *fakeConnection
	payload []byte
	message *nex.RMCMessage
}

func (p *fakePacket) Sender() nex.ConnectionInterface       { return p.sender }
func (p *fakePacket) Payload() []byte                       { return p.payload }
func (p *fakePacket) SetPayload(payload []byte)             { p.payload = payload }
func (p *fakePacket) RMCMessage() *nex.RMCMessage           { return p.message }
func (p *fakePacket) SetRMCMessage(message *nex.RMCMessage) { p.message = message }

// * A request sent by the player with the given PID
func from(pid types.PID) nex.PacketInterface {
	return &fakePacket{sender: &fakeConnection{pid: pid}}
}

// * Makes links without signing anything, and remembers how long each
// * was asked to last
type fakePresigner struct {
	mutex     sync.Mutex
	failGet   bool
	failPost  bool
	lifetimes []time.Duration
}

func (p *fakePresigner) GetObject(bucket, key string, lifetime time.Duration) (*url.URL, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.lifetimes = append(p.lifetimes, lifetime)

	if p.failGet {
		return nil, errors.New("no link can be made")
	}

	return url.Parse("https://files.test/" + bucket + "/" + key)
}

func (p *fakePresigner) PostObject(bucket, key string, lifetime time.Duration) (*url.URL, map[string]string, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.lifetimes = append(p.lifetimes, lifetime)

	if p.failPost {
		return nil, nil, errors.New("no link can be made")
	}

	link, err := url.Parse("https://files.test/" + bucket)

	return link, map[string]string{"key": key, "policy": "signed"}, err
}

// * Stands in for S3, answering only the question of how big a file is
type fakeFileStorage struct {
	mutex sync.Mutex
	sizes map[string]int64
}

// * Puts a file of the given size at a key in the test bucket
func (f *fakeFileStorage) put(key string, size int64) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	f.sizes["/"+testBucket+"/"+key] = size
}

func (f *fakeFileStorage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mutex.Lock()
	size, found := f.sizes[r.URL.Path]
	f.mutex.Unlock()

	if r.Method != http.MethodHead || !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("ETag", `"d41d8cd98f00b204e9800998ecf8427e"`)
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
}

// * Everything a handler reaches for, set up for one test
type testServer struct {
	store     *sqlite.Store
	presigner *fakePresigner
	files     *fakeFileStorage
	// * Friends of each player, for objects only friends may see
	friends map[uint32][]uint32
}

// * Sets up the globals the handlers use, as the real server does in
// * nex/secure.go and nex/register_common_secure_server_protocols.go,
// * with a fresh SQLite store. Everything is put back after the test
func setUp(t *testing.T) *testServer {
	t.Helper()

	previousLogger := globals.Logger
	previousServer := globals.SecureServer
	previousStore := globals.Store
	previousPresigner := globals.Presigner
	previousMinIO := globals.MinIOClient
	previousRootCA := globals.FileRootCA
	previousCommon := globals.DatastoreCommon

	t.Cleanup(func() {
		globals.Logger = previousLogger
		globals.SecureServer = previousServer
		globals.Store = previousStore
		globals.Presigner = previousPresigner
		globals.MinIOClient = previousMinIO
		globals.FileRootCA = previousRootCA
		globals.DatastoreCommon = previousCommon
	})

	globals.Logger = plogger.NewLogger()

	globals.SecureServer = nex.NewPRUDPServer()
	globals.SecureServer.ByteStreamSettings.UseStructureHeader = true
	globals.SecureServer.LibraryVersions.SetDefault(nex.NewLibraryVersion(3, 8, 3))

	opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { opened.Close() })

	server := &testServer{
		store:     opened,
		presigner: &fakePresigner{},
		files:     &fakeFileStorage{sizes: map[string]int64{}},
		friends:   map[uint32][]uint32{},
	}

	globals.Store = opened
	globals.Presigner = server.presigner
	globals.FileRootCA = testRootCA

	files := httptest.NewServer(server.files)
	t.Cleanup(files.Close)

	globals.MinIOClient, err = minio.New(strings.TrimPrefix(files.URL, "http://"), &minio.Options{
		Creds:  credentials.NewStaticV4("test", "test", ""),
		Region: "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	globals.DatastoreCommon = &datastorecommon.CommonProtocol{
		GetUserFriendPIDs: func(pid uint32) []uint32 { return server.friends[pid] },
	}

	t.Setenv("PN_SMM_CONFIG_S3_BUCKET", testBucket)
	t.Setenv("PN_SMM_CONFIG_S3_KEY_BASE", testKeyBase)
	t.Setenv("PN_SMM_LOG_LOOKUPS", "")

	return server
}

// * Swaps in a store whose database has been closed, so that every
// * query fails
func useBrokenStore(t *testing.T) {
	t.Helper()

	broken, err := sqlite.Open(filepath.Join(t.TempDir(), "broken.db"))
	if err != nil {
		t.Fatal(err)
	}

	broken.Close()
	globals.Store = broken
}

// * A store that fails some of the course searches, to reach what a
// * handler does when only part of what it asks for goes wrong
type faultyStore struct {
	store.Store
	failAnyCourses     bool
	failRatedCourses   bool
	failUnratedCourses bool
}

func (s *faultyStore) GetRandomCoursesWithLimit(limit int) (types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult], *nex.Error) {
	if s.failAnyCourses {
		return nil, nex.NewError(nex.ResultCodes.DataStore.Unknown, "any courses failed")
	}

	return s.Store.GetRandomCoursesWithLimit(limit)
}

func (s *faultyStore) GetRandomCoursesByFailureRate(limit int, filter store.FailureRateFilter) (types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult], *nex.Error) {
	if filter.Unrated && s.failUnratedCourses || !filter.Unrated && s.failRatedCourses {
		return nil, nex.NewError(nex.ResultCodes.DataStore.Unknown, "courses by failure rate failed")
	}

	return s.Store.GetRandomCoursesByFailureRate(limit, filter)
}

// * What an object is made with. The zero value is a course owned by
// * player 1000 that anyone may see
type object struct {
	owner       types.PID
	dataType    uint16
	name        string
	size        uint32
	tags        []string
	metaBinary  []byte
	permission  uint8
	recipients  []types.PID
	ratingSlots int
	// * Left as a game leaves an object it has not finished uploading
	notUploaded bool
}

func addObject(t *testing.T, s store.Store, o object) types.UInt64 {
	t.Helper()

	if o.owner == 0 {
		o.owner = 1000
	}

	if o.dataType == 0 {
		o.dataType = 9
	}

	param := datastore_types.NewDataStorePreparePostParam()
	param.DataType = types.UInt16(o.dataType)
	param.Name = types.NewString(o.name)
	param.Size = types.UInt32(o.size)
	param.MetaBinary = types.NewQBuffer(o.metaBinary)
	param.Permission.Permission = types.UInt8(o.permission)
	param.Permission.RecipientIDs = types.List[types.PID](o.recipients)

	for _, tag := range o.tags {
		param.Tags = append(param.Tags, types.NewString(tag))
	}

	id, nexError := s.InitializeObjectByPreparePostParam(o.owner, param)
	if nexError != nil {
		t.Fatal(nexError)
	}

	for slot := range o.ratingSlots {
		rating := datastore_types.NewDataStoreRatingInitParamWithSlot()
		rating.Slot = types.Int8(slot)

		if nexError := s.InitializeObjectRatingWithSlot(id, rating); nexError != nil {
			t.Fatal(nexError)
		}
	}

	if !o.notUploaded {
		if nexError := s.UpdateObjectUploadCompletedByDataID(types.UInt64(id), true); nexError != nil {
			t.Fatal(nexError)
		}
	}

	return types.UInt64(id)
}

// * Makes a player's maker object, as a real game does: data type 1
// * with 9 rating slots
func addMaker(t *testing.T, s store.Store, owner types.PID) types.UInt64 {
	t.Helper()

	return addObject(t, s, object{owner: owner, dataType: 1, name: "maker", ratingSlots: 9})
}

// * Makes a course with 5 rating slots, as a real game does, and puts it
// * in the stars ranking with the given stars
func addRankedCourse(t *testing.T, s store.Store, o object, stars uint32) types.UInt64 {
	t.Helper()

	if o.ratingSlots == 0 {
		o.ratingSlots = 5
	}

	dataID := addObject(t, s, o)

	if nexError := s.InsertOrUpdateCustomRanking(dataID, 0, types.UInt32(stars)); nexError != nil {
		t.Fatal(nexError)
	}

	return dataID
}

// * Marks an object deleted, as deleting a course does
func deleteObject(t *testing.T, s *sqlite.Store, dataID types.UInt64) {
	t.Helper()

	if nexError := s.DeleteObjectByDataID(dataID); nexError != nil {
		t.Fatal(nexError)
	}
}

// * Puts an object under review, which takes it out of every search
func putUnderReview(t *testing.T, s *sqlite.Store, dataID types.UInt64) {
	t.Helper()

	if _, err := s.DB().Exec(`UPDATE objects SET under_review=1 WHERE data_id=?`, int64(dataID)); err != nil {
		t.Fatal(err)
	}
}

// * A DataID as a game stores it in a buffer queue: 8 bytes, little endian
func dataIDBuffer(dataID types.UInt64) types.QBuffer {
	return types.QBuffer(binary.LittleEndian.AppendUint64(nil, uint64(dataID)))
}

// * Checks a handler succeeded with the method and call it was asked
// * for, and returns what it sent back for reading
func response(t *testing.T, rmc *nex.RMCMessage, nexError *nex.Error, methodID, callID uint32) *nex.ByteStreamIn {
	t.Helper()

	if nexError != nil {
		t.Fatalf("got error %s (0x%08X), want a response", nexError.Message, nexError.ResultCode)
	}

	if rmc == nil {
		t.Fatal("got no response and no error")
	}

	if !rmc.IsSuccess || rmc.ProtocolID != datastore_super_mario_maker.ProtocolID || rmc.MethodID != methodID || rmc.CallID != callID {
		t.Fatalf("got a response to protocol %d method %d call %d (success %v), want protocol %d method %d call %d",
			rmc.ProtocolID, rmc.MethodID, rmc.CallID, rmc.IsSuccess, datastore_super_mario_maker.ProtocolID, methodID, callID)
	}

	return nex.NewByteStreamIn(rmc.Parameters, globals.SecureServer.LibraryVersions, globals.SecureServer.ByteStreamSettings)
}

// * Reads everything a handler sent back, in order, and checks nothing
// * was left over
func read(t *testing.T, stream *nex.ByteStreamIn, values ...interface{ ExtractFrom(types.Readable) error }) {
	t.Helper()

	for i, value := range values {
		if err := value.ExtractFrom(stream); err != nil {
			t.Fatalf("could not read value %d of the response: %v", i, err)
		}
	}

	if left := stream.Remaining(); left != 0 {
		t.Fatalf("%d bytes were left over after reading the response", left)
	}
}

// * Checks a handler failed with the given result code
func wantError(t *testing.T, rmc *nex.RMCMessage, nexError *nex.Error, resultCode uint32) {
	t.Helper()

	if rmc != nil {
		t.Errorf("got a response, want none")
	}

	// * Errors are sent with their error bit set, as nex.NewError does
	resultCode = nex.NewError(resultCode, "").ResultCode

	if nexError == nil {
		t.Fatalf("got no error, want 0x%08X", resultCode)
	}

	if nexError.ResultCode != resultCode {
		t.Errorf("got error 0x%08X (%s), want 0x%08X", nexError.ResultCode, nexError.Message, resultCode)
	}
}

// * Checks every result reports success, and that there is one for each
// * thing sent
func wantAllSucceeded(t *testing.T, results types.List[types.QResult], count int) {
	t.Helper()

	if len(results) != count {
		t.Fatalf("got %d results, want %d", len(results), count)
	}

	for i, result := range results {
		if !result.IsSuccess() {
			t.Errorf("result %d is an error (0x%08X)", i, uint32(result))
		}
	}
}

// * The DataIDs of ranking results, in the order sent
func dataIDsOf(results types.List[datastore_super_mario_maker_types.DataStoreCustomRankingResult]) []types.UInt64 {
	ids := make([]types.UInt64, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.MetaInfo.DataID)
	}

	return ids
}

func sameIDs(got []types.UInt64, want ...types.UInt64) bool {
	if len(got) != len(want) {
		return false
	}

	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}

	return true
}

// * Ratings by slot, as totals
func ratingTotals(ratings types.List[datastore_types.DataStoreRatingInfoWithSlot]) map[int8]int64 {
	totals := make(map[int8]int64, len(ratings))
	for _, rating := range ratings {
		totals[int8(rating.Slot)] = int64(rating.Rating.TotalValue)
	}

	return totals
}
