package seed_test

import (
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/coursefile"
	"github.com/PretendoNetwork/super-mario-maker/seed"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

func courseFile(t *testing.T, name string) []byte {
	t.Helper()

	course := &coursefile.Course{
		Preview:   make([]byte, coursefile.PictureSize),
		MainArea:  make([]byte, coursefile.AreaSize),
		SubArea:   make([]byte, coursefile.AreaSize),
		Thumbnail: make([]byte, coursefile.PictureSize),
	}

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
	store  store.Store
	seeder *seed.Seeder
	files  map[string][]byte
	player uint32
	// * For checks the storage interface has no question for
	database *sql.DB
}

// * A server with one player who has entered Course World, which is
// * what makes the maker object the Mii is copied from
func newSetup(t *testing.T) *setup {
	t.Helper()

	opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { opened.Close() })

	player, err := opened.CreateAccount(store.NewAccount{Username: "RealPlayer", MiiName: "Real"})
	if err != nil {
		t.Fatal(err)
	}

	param := datastore_types.NewDataStorePreparePostParam()
	param.Name = types.NewString("RealPlayer")
	param.DataType = 1
	param.MetaBinary = types.NewQBuffer([]byte("the Mii of the real player"))
	param.Flag = 256
	param.Period = 90

	makerID, nexError := opened.InitializeObjectByPreparePostParam(types.PID(player.PID), param)
	if nexError != nil {
		t.Fatal(nexError)
	}

	if nexError := opened.UpdateObjectUploadCompletedByDataID(types.UInt64(makerID), true); nexError != nil {
		t.Fatal(nexError)
	}

	s := &setup{store: opened, files: map[string][]byte{}, player: player.PID, database: opened.DB()}
	s.seeder = &seed.Seeder{
		Store:   opened,
		KeyBase: "courses",
		Random:  rand.New(rand.NewPCG(1, 1)),
		PutFile: func(key string, data []byte) error {
			s.files[key] = data
			return nil
		},
	}

	return s
}

func TestCourseNameIsReadFromTheCourse(t *testing.T) {
	for _, name := range []string{"a simpleton walk", "あっちこっち", "", "exactly thirty two characters ok"} {
		course, err := coursefile.Unpack(courseFile(t, name))
		if err != nil {
			t.Fatal(err)
		}

		if got := seed.CourseName(course); got != name {
			t.Errorf("got %q, want %q", got, name)
		}
	}
}

func TestMakersGetAnAccountAndAMakerObjectWithTheCopiedMii(t *testing.T) {
	s := newSetup(t)

	pids, err := s.seeder.Makers("SeedMaker", 3, s.player)
	if err != nil {
		t.Fatal(err)
	}

	if len(pids) != 3 {
		t.Fatalf("got %d makers, want 3", len(pids))
	}

	for i, pid := range pids {
		account, err := s.store.GetAccountByPID(uint32(pid))
		if err != nil {
			t.Fatal(err)
		}

		if want := []string{"SeedMaker01", "SeedMaker02", "SeedMaker03"}[i]; account.Username != want {
			t.Errorf("maker %d is %s, want %s", i, account.Username, want)
		}

		if account.PasswordHash != "" {
			t.Errorf("%s has a password, so somebody could log in as it", account.Username)
		}

		target := datastore_types.NewDataStorePersistenceTarget()
		target.OwnerID = pid

		maker, nexError := s.store.GetObjectInfoByPersistenceTargetWithPassword(target, 0)
		if nexError != nil {
			t.Fatalf("%s has no maker object: %v", account.Username, nexError)
		}

		// * The stand-in Mii of the test player is too short to rename, so it is copied as it is
		if string(maker.MetaBinary) != "the Mii of the real player" || maker.DataType != 1 {
			t.Errorf("%s: maker object has type %d and Mii %q", account.Username, maker.DataType, maker.MetaBinary)
		}

		if len(maker.Ratings) != 9 {
			t.Errorf("%s: maker object has %d rating slots, want 9", account.Username, len(maker.Ratings))
		}
	}
}

func TestMakersCanBeAskedForAgainWithoutMakingMore(t *testing.T) {
	s := newSetup(t)

	first, err := s.seeder.Makers("SeedMaker", 2, s.player)
	if err != nil {
		t.Fatal(err)
	}

	second, err := s.seeder.Makers("SeedMaker", 3, s.player)
	if err != nil {
		t.Fatal(err)
	}

	if second[0] != first[0] || second[1] != first[1] {
		t.Errorf("the makers changed: %v then %v", first, second)
	}

	// * The real player's maker object, and one for each of the three makers
	var makerObjects int
	if err := s.database.QueryRow(`SELECT COUNT(*) FROM objects WHERE data_type=1`).Scan(&makerObjects); err != nil {
		t.Fatal(err)
	}

	if makerObjects != 4 {
		t.Errorf("there are %d maker objects, want 4. Asking again must not give a maker a second one", makerObjects)
	}
}

func TestMakersNeedAPlayerWhoHasEnteredCourseWorld(t *testing.T) {
	s := newSetup(t)

	if _, err := s.seeder.Makers("SeedMaker", 1, s.player+500); err == nil {
		t.Error("makers were made with no Mii to copy")
	}
}

func TestCourseIsListedWithItsFileNameOwnerAndNumbers(t *testing.T) {
	s := newSetup(t)

	pids, err := s.seeder.Makers("SeedMaker", 1, s.player)
	if err != nil {
		t.Fatal(err)
	}

	file := courseFile(t, "Seeded Course")
	stats := seed.Stats{Plays: 7, Clears: 3, Attempts: 11, Fails: 8, Stars: 4}

	dataID, err := s.seeder.Course(file, pids[0], stats)
	if err != nil {
		t.Fatal(err)
	}

	if dataID < store.FirstDataID {
		t.Errorf("course got DataID %d, which is in the range kept for official courses", dataID)
	}

	if stored := s.files["courses/"+types.UInt64(dataID).String()+".bin"]; string(stored) != string(file) {
		t.Errorf("the file was not stored under the key the server hands out. Keys: %v", keys(s.files))
	}

	owned, nexError := s.store.GetUserCourseObjectIDs(pids[0])
	if nexError != nil || len(owned) != 1 || uint64(owned[0]) != dataID {
		t.Errorf("the maker's courses are %v (%v), want only %d", owned, nexError, dataID)
	}

	listed, nexError := s.store.GetRandomCoursesWithLimit(10)
	if nexError != nil {
		t.Fatal(nexError)
	}

	if len(listed) != 1 || uint64(listed[0].MetaInfo.DataID) != dataID {
		t.Fatalf("Course World lists %d courses, want the seeded one", len(listed))
	}

	info := listed[0].MetaInfo
	if string(info.Name) != "Seeded Course" || info.OwnerID != pids[0] || int(info.Size) != len(file) {
		t.Errorf("listed as %q by %d with size %d", info.Name, info.OwnerID, info.Size)
	}

	// * Without the meta binary a game loads the course with nothing in it
	wantMeta, err := coursefile.MetaBinary(file)
	if err != nil {
		t.Fatal(err)
	}

	if string(info.MetaBinary) != string(wantMeta) || info.DataType != 9 || info.Flag != 3840 {
		t.Errorf("stored with type %d, flag %d and meta binary %x", info.DataType, info.Flag, []byte(info.MetaBinary))
	}

	if listed[0].Score != 4 {
		t.Errorf("listed with %d stars, want 4", listed[0].Score)
	}

	want := map[int8]int64{seed.SlotPlays: 7, seed.SlotClears: 3, seed.SlotAttempts: 11, seed.SlotFails: 8}
	if len(info.Ratings) != 7 {
		t.Errorf("course has %d rating slots, want 7", len(info.Ratings))
	}

	for _, rating := range info.Ratings {
		slot := int8(rating.Slot)
		if int64(rating.Rating.TotalValue) != want[slot] || int64(rating.Rating.Count) != want[slot] {
			t.Errorf("slot %d holds total %d and count %d, want both %d", slot, rating.Rating.TotalValue, rating.Rating.Count, want[slot])
		}
	}
}

func TestCourseThatIsNotACourseFileIsNotAdded(t *testing.T) {
	s := newSetup(t)

	if _, err := s.seeder.Course([]byte("not a course"), types.PID(s.player), seed.Stats{}); !errors.Is(err, coursefile.ErrNotACourseFile) {
		t.Errorf("got %v, want ErrNotACourseFile", err)
	}

	if listed, _ := s.store.GetRandomCoursesWithLimit(10); len(listed) != 0 || len(s.files) != 0 {
		t.Errorf("something was added: %d courses, %d files", len(listed), len(s.files))
	}
}

func TestCourseWhoseFileCanNotBeStoredIsNotListed(t *testing.T) {
	s := newSetup(t)
	s.seeder.PutFile = func(string, []byte) error { return errors.New("storage is down") }

	if _, err := s.seeder.Course(courseFile(t, "Lost"), types.PID(s.player), seed.Stats{}); err == nil {
		t.Fatal("no error when the file could not be stored")
	}

	if listed, _ := s.store.GetRandomCoursesWithLimit(10); len(listed) != 0 {
		t.Error("a course with no file is listed in Course World")
	}
}

func TestMadeUpStatsAddUp(t *testing.T) {
	s := newSetup(t)

	easy, hard := 0, 0

	for range 2000 {
		stats := s.seeder.MakeStats()

		if stats.Clears+stats.Fails != stats.Attempts || stats.Clears < 0 || stats.Fails < 0 || stats.Attempts < 1 {
			t.Fatalf("attempts do not add up: %+v", stats)
		}

		if stats.Plays < 1 || stats.Plays > stats.Attempts+1 || stats.Stars < 0 || stats.Stars > stats.Plays {
			t.Fatalf("plays or stars out of range: %+v", stats)
		}

		if stats.Clears*2 > stats.Attempts {
			easy++
		}

		if stats.Clears*10 < stats.Attempts {
			hard++
		}
	}

	if easy < 200 || hard < 200 {
		t.Errorf("of 2000 courses %d are easy and %d are hard, want plenty of both", easy, hard)
	}
}

func keys(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	return names
}

// * The meta binary of the maker object a real game made on a test
// * server, for an account whose Mii was named "default"
const realMakerMetaBinary = "425046430000000100000000000000000000000000010000010001100000d73e030034" +
	"330100010001000100010000000100640065006600610075006c007400000000000000" +
	"0100010001000100010001000106010001000100010001000100010001000100010001" +
	"0001000100010001000100000010b10000000000000000000000000000000000000001"

func TestMiiChecksumMatchesTheOneInARealMii(t *testing.T) {
	meta, err := hex.DecodeString(realMakerMetaBinary)
	if err != nil || len(meta) != 140 {
		t.Fatalf("the real meta binary did not decode to 140 bytes: %d, %v", len(meta), err)
	}

	mii := meta[0x18 : 0x18+96]

	if got, want := seed.MiiChecksum(mii[:94]), uint16(0x10b1); got != want {
		t.Errorf("checksum is %04x, the real Mii has %04x", got, want)
	}
}

func TestMakerMetaBinaryRenamesTheMiiAndKeepsItValid(t *testing.T) {
	template, _ := hex.DecodeString(realMakerMetaBinary)

	renamed := seed.MakerMetaBinary(template, "SeedMaker01", 1800000002)

	if len(renamed) != len(template) {
		t.Fatalf("the meta binary changed size from %d to %d", len(template), len(renamed))
	}

	mii := renamed[0x18 : 0x18+96]

	// * A Mii name holds 10 characters, so the 11th is dropped
	name := make([]uint16, 10)
	for i := range name {
		name[i] = binary.LittleEndian.Uint16(mii[0x1A+i*2:])
	}

	if got := string(utf16.Decode(name)); got != "SeedMaker0" {
		t.Errorf("the Mii is named %q, want SeedMaker0", got)
	}

	if binary.BigEndian.Uint16(mii[94:]) != seed.MiiChecksum(mii[:94]) {
		t.Error("the checksum of the renamed Mii is wrong, so a game would refuse it")
	}

	other := seed.MakerMetaBinary(template, "SeedMaker02", 1800000003)
	if string(other[0x18+8:0x18+12]) == string(mii[8:12]) {
		t.Error("two makers were given the same Mii ID")
	}

	// * Only the Mii's name, ID and checksum may change
	for i := range template {
		inName := i >= 0x18+0x1A && i < 0x18+0x1A+20
		inID := i >= 0x18+8 && i < 0x18+12
		inChecksum := i >= 0x18+94 && i < 0x18+96

		if !inName && !inID && !inChecksum && renamed[i] != template[i] {
			t.Fatalf("byte %#x changed from %02x to %02x", i, template[i], renamed[i])
		}
	}

	if string(template[0x32:0x34]) != "d\x00" {
		t.Error("the template was changed")
	}

	short := seed.MakerMetaBinary([]byte("too short"), "Name", 1)
	if string(short) != "too short" {
		t.Errorf("a meta binary with no room for a Mii became %q", short)
	}
}
