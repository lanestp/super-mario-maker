// Package seed fills a server with made up makers and course statistics
// around real course files, so that a private server has something in
// Course World to try out.
//
// Nothing here is copied from another server. The makers, who made which
// course and every number are made up. Only the course files and the
// course names inside them are real.
//
// The course objects are made the way a real game was seen to make one.
// What is still a best guess is marked GUESS
package seed

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/PretendoNetwork/super-mario-maker/mii"
	"math/rand/v2"
	"unicode/utf16"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/coursefile"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

const (
	// * What a real game sent for the maker object it made on a test server
	makerDataType    = 1
	makerRatingSlots = 9
	objectFlag       = 256
	objectPeriod     = 90
	deletePermission = 3

	// * What a real game sent for a course it uploaded to a test server
	courseDataType    = 9
	courseFlag        = 3840
	courseRatingSlots = 7
	// * A course is not kept in one of its maker's numbered slots
	noPersistenceSlot = 65535

	// * GUESS. Which rating slot holds which number, as read from the
	// * SmmServer project. Not checked against a game
	SlotPlays    = 0
	SlotClears   = 2
	SlotAttempts = 3
	SlotFails    = 4

	// * The ranking a course needs to be listed in Course World, and
	// * which holds its stars
	starsApplicationID = 0

	// * The two rankings a real game was seen to make for its own maker
	// * object when it entered Course World, both starting at 0. A game
	// * asks for the first when it shows a maker, and shows nothing for a
	// * maker that is not in it.
	// *
	// * GUESS. The first looks like the stars a maker has been given in
	// * all, and the second the same for a shorter time. Stars given to
	// * a made up course are added to the first
	makerStarsApplicationID       = 300000000
	makerRecentStarsApplicationID = 300002400

	// * Where the course name is in the main area: 33 UTF-16 characters
	nameOffset = 0x28
	nameLength = 66
)

// * The extra data a real game sent with a course. It looks like the
// * console, a version, the region and three unused values
func courseExtraData() types.List[types.String] {
	return types.List[types.String]{"WUP", "2", "USA", "0", "", ""}
}

// Stats are the numbers shown for a course
type Stats struct {
	Plays    int
	Clears   int
	Attempts int
	Fails    int
	Stars    int
}

// Seeder adds makers and courses to a server
type Seeder struct {
	Store store.Store
	// PutFile stores a course file where the server hands it out from
	PutFile func(key string, data []byte) error
	// KeyBase is the key prefix the server keeps course files under
	KeyBase string
	Random  *rand.Rand
}

// * Where the Mii is in the meta binary of a maker object, as read from
// * one a real game made
const makerMiiOffset = 0x18

// MakerMetaBinary returns a copy of the meta binary of a maker object
// with the Mii inside it renamed and given its own ID, so that makers
// that share a face are still told apart. A meta binary too short to
// hold a Mii is returned as it is
func MakerMetaBinary(template []byte, name string, id uint32) []byte {
	meta := append([]byte{}, template...)
	if len(meta) < makerMiiOffset+mii.Size {
		return meta
	}

	copy(meta[makerMiiOffset:], mii.Renamed(meta[makerMiiOffset:makerMiiOffset+mii.Size], name, id))

	return meta
}

// CourseName reads the name of a course from its main area
func CourseName(course *coursefile.Course) string {
	raw := course.MainArea[nameOffset : nameOffset+nameLength]
	units := make([]uint16, 0, nameLength/2)

	for i := 0; i+1 < len(raw); i += 2 {
		unit := uint16(raw[i])<<8 | uint16(raw[i+1])
		if unit == 0 {
			break
		}

		units = append(units, unit)
	}

	return string(utf16.Decode(units))
}

// MakeStats makes up numbers for a course which add up: every attempt is
// a clear or a fail, nobody clears more often than they try, and no more
// stars are given than there are players
func (s *Seeder) MakeStats() Stats {
	attempts := 5 + s.Random.IntN(120)
	// * Squared, so that most courses are hard and a few are easy, as
	// * 100 Mario needs courses of every difficulty
	chance := s.Random.Float64()
	clears := int(float64(attempts) * chance * chance)
	plays := 1 + clears + s.Random.IntN(attempts-clears+1)/2

	return Stats{
		Plays:    plays,
		Clears:   clears,
		Attempts: attempts,
		Fails:    attempts - clears,
		Stars:    s.Random.IntN(plays + 1),
	}
}

// Makers makes sure accounts named <prefix>01, <prefix>02 and so on exist
// and each have a maker object, and returns their PIDs. The Mii of every
// maker is copied from the maker object of the account miiFrom, as a
// maker without a Mii can not be shown. The accounts have no password,
// so nobody can log in as them
func (s *Seeder) Makers(prefix string, count int, miiFrom uint32) ([]types.PID, error) {
	template, nexError := s.makerOf(types.PID(miiFrom))
	if nexError != nil {
		return nil, fmt.Errorf("the account to copy a Mii from has no maker object. Enter Course World with it once first: %v", nexError)
	}

	pids := make([]types.PID, 0, count)

	for i := 1; i <= count; i++ {
		username := fmt.Sprintf("%s%02d", prefix, i)

		account, err := s.Store.GetAccountByUsername(username)
		if errors.Is(err, store.ErrAccountNotFound) {
			account, err = s.Store.CreateAccount(store.NewAccount{Username: username, MiiName: username})
		}

		if err != nil {
			return nil, fmt.Errorf("account %s: %w", username, err)
		}

		pid := types.PID(account.PID)
		pids = append(pids, pid)

		if maker, nexError := s.makerOf(pid); nexError == nil {
			// * A maker made before rankings were added gets them now.
			// * Adding 0 leaves a ranking that is there as it is
			if err := s.addMakerRankings(uint64(maker.DataID)); err != nil {
				return nil, err
			}

			continue
		}

		param := datastore_types.NewDataStorePreparePostParam()
		param.Name = types.NewString(username)
		param.DataType = makerDataType
		param.MetaBinary = types.NewQBuffer(MakerMetaBinary(template.MetaBinary, username, uint32(pid)))
		param.Permission = template.Permission
		param.DelPermission = template.DelPermission
		param.Flag = template.Flag
		param.Period = template.Period
		param.Tags = template.Tags

		makerID, err := s.addObject(pid, param, makerRatingSlots)
		if err != nil {
			return nil, fmt.Errorf("maker object of %s: %w", username, err)
		}

		if err := s.addMakerRankings(makerID); err != nil {
			return nil, err
		}
	}

	return pids, nil
}

func (s *Seeder) addMakerRankings(makerID uint64) error {
	for _, applicationID := range []types.UInt32{makerStarsApplicationID, makerRecentStarsApplicationID} {
		if nexError := s.Store.InsertOrUpdateCustomRanking(types.UInt64(makerID), applicationID, 0); nexError != nil {
			return nexError
		}
	}

	return nil
}

func (s *Seeder) makerOf(pid types.PID) (datastore_types.DataStoreMetaInfo, *nex.Error) {
	target := datastore_types.NewDataStorePersistenceTarget()
	target.OwnerID = pid
	target.PersistenceSlotID = 0

	return s.Store.GetObjectInfoByPersistenceTargetWithPassword(target, 0)
}

// * Adds an object with empty rating slots, and marks it as uploaded
func (s *Seeder) addObject(owner types.PID, param datastore_types.DataStorePreparePostParam, ratingSlots int) (uint64, error) {
	dataID, nexError := s.Store.InitializeObjectByPreparePostParam(owner, param)
	if nexError != nil {
		return 0, nexError
	}

	return dataID, s.finishObject(dataID, ratingSlots)
}

func (s *Seeder) finishObject(dataID uint64, ratingSlots int) error {
	for slot := range ratingSlots {
		rating := datastore_types.NewDataStoreRatingInitParamWithSlot()
		rating.Slot = types.Int8(slot)

		if nexError := s.Store.InitializeObjectRatingWithSlot(dataID, rating); nexError != nil {
			return nexError
		}
	}

	if nexError := s.Store.UpdateObjectUploadCompletedByDataID(types.UInt64(dataID), true); nexError != nil {
		return nexError
	}

	return nil
}

// * The record of a course that holds who cleared it first and who
// * cleared it fastest. A real game was seen to store a clear time here in
// * thousandths of a second. A game shows "first clear" for a course
// * that has no record
const clearRecordSlot = 0

// ClearRecord gives a course a first clear by one player and a world
// record by another, the way two games clearing it would. The times are
// made up: the first clear takes between 30 and 300 seconds, and the
// record is faster. Use it only for courses whose numbers say they have
// been cleared
func (s *Seeder) ClearRecord(course uint64, first, fastest types.PID) error {
	firstTime := 30_000 + s.Random.IntN(270_000)
	recordTime := firstTime - 1 - s.Random.IntN(firstTime/3)

	if nexError := s.Store.InsertOrUpdateCourseRecord(types.UInt64(course), clearRecordSlot, first, types.Int32(firstTime)); nexError != nil {
		return nexError
	}

	if nexError := s.Store.InsertOrUpdateCourseRecord(types.UInt64(course), clearRecordSlot, fastest, types.Int32(recordTime)); nexError != nil {
		return nexError
	}

	return nil
}

// * The list on a maker object that holds the courses its maker starred.
// * Each entry is the DataID of a course as 8 little endian bytes
const starredCoursesSlot = 0

// Star puts up to count of the courses, picked at random, in the list
// of courses the maker has starred. A maker does not star their own
// courses. The same courses are picked each time for the same maker, so
// doing this again does not grow the list. It returns how many were starred
func (s *Seeder) Star(maker types.PID, courses []uint64, count int) (int, error) {
	makerObject, nexError := s.makerOf(maker)
	if nexError != nil {
		return 0, fmt.Errorf("maker %d has no maker object: %v", maker, nexError)
	}

	random := rand.New(rand.NewPCG(uint64(maker), starredCoursesSlot))
	starred := 0

	for _, pick := range random.Perm(len(courses)) {
		if starred == count {
			break
		}

		owner, nexError := s.Store.GetObjectOwnerByDataID(types.UInt64(courses[pick]))
		if nexError != nil {
			return starred, nexError
		}

		if types.PID(owner) == maker {
			continue
		}

		entry := binary.LittleEndian.AppendUint64(nil, courses[pick])

		if nexError := s.Store.InsertOrUpdateBufferQueueData(makerObject.DataID, starredCoursesSlot, types.NewQBuffer(entry)); nexError != nil {
			return starred, nexError
		}

		starred++
	}

	return starred, nil
}

// Course adds a course file to the server as made by owner, with the
// given numbers, and returns its DataID. The file must be one a game
// could have uploaded
func (s *Seeder) Course(file []byte, owner types.PID, stats Stats) (uint64, error) {
	course, err := coursefile.Unpack(file)
	if err != nil {
		return 0, err
	}

	metaBinary, err := coursefile.MetaBinary(file)
	if err != nil {
		return 0, err
	}

	param := datastore_types.NewDataStorePreparePostParam()
	param.Size = types.UInt32(len(file))
	// * The one course seen uploaded by a real game had no name in its
	// * file and none on the server, so a game seems to send the name
	// * that is in the file
	param.Name = types.NewString(CourseName(course))
	param.DataType = courseDataType
	param.MetaBinary = types.NewQBuffer(metaBinary)
	param.DelPermission.Permission = deletePermission
	param.Flag = courseFlag
	param.ExtraData = courseExtraData()
	param.Period = objectPeriod
	param.PersistenceInitParam.PersistenceSlotID = noPersistenceSlot

	dataID, nexError := s.Store.InitializeObjectByPreparePostParam(owner, param)
	if nexError != nil {
		return 0, nexError
	}

	// * The file is stored before the course is marked as uploaded, so
	// * that a course is never listed without its file
	if err := s.PutFile(fmt.Sprintf("%s/%d.bin", s.KeyBase, dataID), file); err != nil {
		return 0, fmt.Errorf("storing the file of course %d: %w", dataID, err)
	}

	if err := s.finishObject(dataID, courseRatingSlots); err != nil {
		return 0, err
	}

	// * Each number is added one at a time, the way games add them, so
	// * that the total and the count of a slot agree
	for slot, value := range map[int]int{SlotPlays: stats.Plays, SlotClears: stats.Clears, SlotAttempts: stats.Attempts, SlotFails: stats.Fails} {
		for range value {
			if _, nexError := s.Store.RateObjectWithPassword(types.UInt64(dataID), types.UInt8(slot), 1, 0); nexError != nil {
				return 0, nexError
			}
		}
	}

	if nexError := s.Store.InsertOrUpdateCustomRanking(types.UInt64(dataID), starsApplicationID, types.UInt32(stats.Stars)); nexError != nil {
		return 0, nexError
	}

	// * The stars of a course count towards its maker, if the owner has
	// * a maker object
	if maker, nexError := s.makerOf(owner); nexError == nil {
		if nexError := s.Store.InsertOrUpdateCustomRanking(maker.DataID, makerStarsApplicationID, types.UInt32(stats.Stars)); nexError != nil {
			return 0, nexError
		}
	}

	return dataID, nil
}
