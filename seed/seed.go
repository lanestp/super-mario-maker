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
	"errors"
	"fmt"
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

		if _, nexError := s.makerOf(pid); nexError == nil {
			continue
		}

		param := datastore_types.NewDataStorePreparePostParam()
		param.Name = types.NewString(username)
		param.DataType = makerDataType
		param.MetaBinary = template.MetaBinary
		param.Permission = template.Permission
		param.DelPermission = template.DelPermission
		param.Flag = template.Flag
		param.Period = template.Period
		param.Tags = template.Tags

		if _, err := s.addObject(pid, param, makerRatingSlots); err != nil {
			return nil, fmt.Errorf("maker object of %s: %w", username, err)
		}
	}

	return pids, nil
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

// Course adds a course file to the server as made by owner, with the
// given numbers, and returns its DataID. The file must be one a game
// could have uploaded
func (s *Seeder) Course(file []byte, owner types.PID, stats Stats) (uint64, error) {
	metaBinary, err := coursefile.MetaBinary(file)
	if err != nil {
		return 0, err
	}

	param := datastore_types.NewDataStorePreparePostParam()
	param.Size = types.UInt32(len(file))
	// * A game sends no name, as the name is in the file
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

	return dataID, nil
}
