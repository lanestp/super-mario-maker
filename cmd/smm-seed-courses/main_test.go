package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/coursefile"
	"github.com/PretendoNetwork/super-mario-maker/internal/s3test"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
	"github.com/minio/minio-go/v7"
)

func courseFile(t *testing.T, name string) []byte {
	t.Helper()

	course := &coursefile.Course{
		Preview:   make([]byte, coursefile.PictureSize),
		MainArea:  make([]byte, coursefile.AreaSize),
		SubArea:   make([]byte, coursefile.AreaSize),
		Thumbnail: make([]byte, coursefile.PictureSize),
	}

	// * Where a course keeps its name
	for i, unit := range utf16.Encode([]rune(name)) {
		course.MainArea[0x28+i*2] = byte(unit >> 8)
		course.MainArea[0x28+i*2+1] = byte(unit)
	}

	file, err := course.Pack()
	if err != nil {
		t.Fatal(err)
	}

	return file
}

type setup struct {
	database  string
	directory string
	s3        *minio.Client
}

// * A server with one player who has entered Course World, an S3 server
// * in memory, and a directory with two course files
func newSetup(t *testing.T) *setup {
	t.Helper()

	s := &setup{database: filepath.Join(t.TempDir(), "smm.db"), directory: t.TempDir()}

	opened, err := sqlite.Open(s.database)
	if err != nil {
		t.Fatal(err)
	}

	defer opened.Close()

	player, err := opened.CreateAccount(store.NewAccount{Username: "RealPlayer", MiiName: "Real"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := opened.CreateAccount(store.NewAccount{Username: "NewPlayer", MiiName: "New"}); err != nil {
		t.Fatal(err)
	}

	// * The maker object a game makes when it enters Course World
	param := datastore_types.NewDataStorePreparePostParam()
	param.Name = types.NewString("RealPlayer")
	param.DataType = 1
	param.MetaBinary = types.NewQBuffer([]byte("the Mii of the real player"))
	param.Period = 90

	makerID, nexError := opened.InitializeObjectByPreparePostParam(types.PID(player.PID), param)
	if nexError != nil {
		t.Fatal(nexError)
	}

	if nexError := opened.UpdateObjectUploadCompletedByDataID(types.UInt64(makerID), true); nexError != nil {
		t.Fatal(nexError)
	}

	server := s3test.Start(t)
	s.s3 = server.Client

	for name, value := range map[string]string{
		"PN_SMM_DATABASE":                "sqlite",
		"PN_SMM_SQLITE_PATH":             s.database,
		"PN_SMM_CONFIG_S3_ENDPOINT":      server.Endpoint,
		"PN_SMM_CONFIG_S3_ACCESS_KEY":    s3test.AccessKey,
		"PN_SMM_CONFIG_S3_ACCESS_SECRET": s3test.SecretKey,
		"PN_SMM_CONFIG_S3_SECURE":        "false",
		"PN_SMM_CONFIG_S3_REGION":        "",
		"PN_SMM_CONFIG_S3_BUCKET":        s3test.Bucket,
		"PN_SMM_CONFIG_S3_KEY_BASE":      "courses",
	} {
		t.Setenv(name, value)
	}

	s.addFile(t, "a.bin", "First course")
	s.addFile(t, "b.bin", "Second course")

	return s
}

func (s *setup) addFile(t *testing.T, name, courseName string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(s.directory, name), courseFile(t, courseName), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (s *setup) seeded(t *testing.T) map[string]uint64 {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(s.directory, "seeded.json"))
	if err != nil {
		t.Fatal(err)
	}

	seeded := map[string]uint64{}
	if err := json.Unmarshal(data, &seeded); err != nil {
		t.Fatal(err)
	}

	return seeded
}

func (s *setup) open(t *testing.T) *sqlite.Store {
	t.Helper()

	opened, err := sqlite.Open(s.database)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { opened.Close() })

	return opened
}

func runPrinting(t *testing.T, directory, miiFrom string, makers int, randomSeed uint64) (string, error) {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	printed := make(chan string)
	go func() {
		data, _ := io.ReadAll(reader)
		printed <- string(data)
	}()

	previous := os.Stdout
	os.Stdout = writer

	runError := run(directory, miiFrom, "SeedMaker", makers, randomSeed)

	os.Stdout = previous
	writer.Close()

	return <-printed, runError
}

func TestSeedingAddsEveryCourseFileWithItsFileInS3(t *testing.T) {
	s := newSetup(t)

	printed, err := runPrinting(t, s.directory, "RealPlayer", 2, 1)
	if err != nil {
		t.Fatal(err)
	}

	seeded := s.seeded(t)
	if len(seeded) != 2 || seeded["a.bin"] == 0 || seeded["b.bin"] == 0 {
		t.Fatalf("seeded.json holds %v, want both files", seeded)
	}

	opened := s.open(t)

	for name, dataID := range seeded {
		info, nexError := opened.GetObjectInfoByDataID(types.UInt64(dataID))
		if nexError != nil {
			t.Errorf("%s: course %d is not on the server: %v", name, dataID, nexError)
			continue
		}

		if !strings.Contains(printed, fmt.Sprintf("%s -> course %d by maker %d: ", name, dataID, info.OwnerID)) {
			t.Errorf("did not print where %s went. Printed:\n%s", name, printed)
		}

		key := fmt.Sprintf("courses/%d.bin", dataID)
		if _, err := s.s3.StatObject(context.Background(), s3test.Bucket, key, minio.StatObjectOptions{}); err != nil {
			t.Errorf("%s: the file is not in S3 as %s: %v", name, key, err)
		}
	}

	// * The made up makers have accounts of their own
	for _, username := range []string{"SeedMaker01", "SeedMaker02"} {
		if _, err := opened.GetAccountByUsername(username); err != nil {
			t.Errorf("maker %s: %v", username, err)
		}
	}

	if !strings.HasSuffix(printed, fmt.Sprintf("Added 2 courses. 2 of the files in %s are on the server\n", s.directory)) {
		t.Errorf("did not say what was added. Printed:\n%s", printed)
	}
}

func TestSeedingAgainOnlyAddsTheFilesThatAreNew(t *testing.T) {
	s := newSetup(t)

	if _, err := runPrinting(t, s.directory, "RealPlayer", 2, 1); err != nil {
		t.Fatal(err)
	}

	first := s.seeded(t)

	printed, err := runPrinting(t, s.directory, "RealPlayer", 2, 1)
	if err != nil {
		t.Fatal(err)
	}

	if printed != fmt.Sprintf("Added 0 courses. 2 of the files in %s are on the server\n", s.directory) {
		t.Errorf("the second run printed %q, want nothing added", printed)
	}

	s.addFile(t, "c.bin", "Third course")

	printed, err = runPrinting(t, s.directory, "RealPlayer", 2, 1)
	if err != nil {
		t.Fatal(err)
	}

	seeded := s.seeded(t)
	if len(seeded) != 3 || seeded["a.bin"] != first["a.bin"] || seeded["b.bin"] != first["b.bin"] {
		t.Errorf("seeded.json holds %v after a file was added to %v", seeded, first)
	}

	if !strings.HasPrefix(printed, "c.bin -> course ") || !strings.HasSuffix(printed, "Added 1 courses. 3 of the files in "+s.directory+" are on the server\n") {
		t.Errorf("the third run printed %q, want only c.bin added", printed)
	}
}

func TestOnlyACourseWithClearsIsGivenAFirstClearAndARecord(t *testing.T) {
	s := newSetup(t)

	for i := range 8 {
		s.addFile(t, fmt.Sprintf("%c.bin", 'c'+i), fmt.Sprintf("Course %d", i+3))
	}

	if _, err := runPrinting(t, s.directory, "RealPlayer", 3, 1); err != nil {
		t.Fatal(err)
	}

	opened := s.open(t)
	cleared, notCleared := 0, 0

	for name, dataID := range s.seeded(t) {
		ratings, nexError := opened.GetObjectRatingsWithSlotByDataID(types.UInt64(dataID))
		if nexError != nil {
			t.Fatalf("%s: %v", name, nexError)
		}

		clears := int64(-1)
		for _, rating := range ratings {
			if rating.Slot == types.Int8(store.RatingSlotClears) {
				clears = int64(rating.Rating.TotalValue)
			}
		}

		_, nexError = opened.GetCourseRecordByDataIDAndSlot(types.UInt64(dataID), 0)
		hasRecord := nexError == nil

		if clears > 0 {
			cleared++
		} else {
			notCleared++
		}

		if hasRecord != (clears > 0) {
			t.Errorf("%s has %d clears and a record: %v", name, clears, hasRecord)
		}
	}

	// * The seed is fixed, so this only changes with how numbers are made up
	if cleared == 0 || notCleared == 0 {
		t.Fatalf("%d courses were cleared and %d were not, want some of each", cleared, notCleared)
	}
}

func TestSeedingWithTheSameSeedMakesUpTheSameNumbers(t *testing.T) {
	statistics := func(seedValue uint64) string {
		s := newSetup(t)

		printed, err := runPrinting(t, s.directory, "RealPlayer", 3, seedValue)
		if err != nil {
			t.Fatal(err)
		}

		// * The last line names the directory, which is different each time
		return printed[:strings.LastIndex(printed, "Added")]
	}

	first, second, other := statistics(7), statistics(7), statistics(8)

	if first != second {
		t.Errorf("the same seed made up different numbers:\n%s\n%s", first, second)
	}

	if first == other {
		t.Errorf("another seed made up the same numbers:\n%s", first)
	}
}

func TestSeedingNeedsAnAccountThatHasEnteredCourseWorld(t *testing.T) {
	s := newSetup(t)

	_, err := runPrinting(t, s.directory, "Nobody", 2, 1)
	if err == nil || err.Error() != "account Nobody: account not found" {
		t.Errorf("an account that is not there gave %v", err)
	}

	_, err = runPrinting(t, s.directory, "NewPlayer", 2, 1)
	if err == nil || !strings.HasPrefix(err.Error(), "the account to copy a Mii from has no maker object. Enter Course World with it once first") {
		t.Errorf("an account without a maker object gave %v", err)
	}

	if _, statError := os.Stat(filepath.Join(s.directory, "seeded.json")); statError == nil {
		t.Error("files were seeded without makers")
	}
}

func TestSeedingNeedsTheBucket(t *testing.T) {
	s := newSetup(t)
	t.Setenv("PN_SMM_CONFIG_S3_BUCKET", "")

	_, err := runPrinting(t, s.directory, "RealPlayer", 2, 1)
	if err == nil || err.Error() != "PN_SMM_CONFIG_S3_BUCKET environment variable not set" {
		t.Errorf("got %v, want the bucket asked for", err)
	}
}

func TestSeedingRefusesADatabaseItDoesNotKnow(t *testing.T) {
	s := newSetup(t)
	t.Setenv("PN_SMM_DATABASE", "mysql")

	_, err := runPrinting(t, s.directory, "RealPlayer", 2, 1)
	if err == nil || err.Error() != "PN_SMM_DATABASE is not a supported database. Expected postgres or sqlite, got mysql" {
		t.Errorf("got %v, want the database refused", err)
	}
}

func TestSeedingNeedsCourseFiles(t *testing.T) {
	// * Sets up the database and S3, and a directory that is not used
	newSetup(t)
	empty := t.TempDir()

	_, err := runPrinting(t, empty, "RealPlayer", 2, 1)
	if err == nil || err.Error() != "there are no .bin course files in "+empty {
		t.Errorf("an empty directory gave %v", err)
	}
}

func TestSeedingStopsAtAFileThatIsNotACourseAndKeepsWhatWasAdded(t *testing.T) {
	s := newSetup(t)

	if err := os.WriteFile(filepath.Join(s.directory, "ab-broken.bin"), []byte("not a course"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runPrinting(t, s.directory, "RealPlayer", 2, 1)
	if err == nil || !strings.HasPrefix(err.Error(), "ab-broken.bin: ") {
		t.Fatalf("got %v, want ab-broken.bin named", err)
	}

	// * a.bin comes before ab-broken.bin, and b.bin after it
	seeded := s.seeded(t)
	if len(seeded) != 1 || seeded["a.bin"] == 0 {
		t.Errorf("seeded.json holds %v, want only a.bin", seeded)
	}
}

func TestSeedingRefusesAListOfSeededFilesItCanNotRead(t *testing.T) {
	s := newSetup(t)

	if err := os.WriteFile(filepath.Join(s.directory, "seeded.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runPrinting(t, s.directory, "RealPlayer", 2, 1)
	if err == nil || !strings.HasPrefix(err.Error(), "seeded.json: ") {
		t.Errorf("got %v, want seeded.json named", err)
	}
}
