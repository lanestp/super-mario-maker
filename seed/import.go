package seed

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/coursefile"
	"github.com/PretendoNetwork/super-mario-maker/mii"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

// ErrAlreadyImported is returned when a course with the same DataID is
// already on the server
var ErrAlreadyImported = errors.New("the course is already on the server")

// ErrInvalidCourse is returned for a course that can not be imported as
// it is. Nothing of it is kept
var ErrInvalidCourse = errors.New("the course can not be imported")

// ArchivedPlayer is a player as an archive of Nintendo's servers has them
type ArchivedPlayer struct {
	// PID is the player's ID on Nintendo's servers. 0 when not known
	PID  uint32
	Name string
	// MakerMetaBinary is the meta binary of their maker object, which
	// holds their Mii. Left out, or not a valid one, a stand-in Mii with
	// their name is used
	MakerMetaBinary []byte
}

// ArchivedRecord is the fastest clear of a course
type ArchivedRecord struct {
	Milliseconds int
	Holder       ArchivedPlayer
}

// ArchivedCourse is a course from an archive of Nintendo's servers
type ArchivedCourse struct {
	// DataID is the course's ID on Nintendo's servers, which it keeps here
	DataID   uint64
	File     []byte
	Name     string
	Uploaded time.Time
	Maker    ArchivedPlayer
	Stars    int
	Clears   int
	Attempts int
	Record   *ArchivedRecord
}

// * The meta binary of the maker object a real game made, holding the Mii
// * Cemu gives a new account. A stand-in for a maker with no usable Mii
const fallbackMakerMetaBinary = "425046430000000100000000000000000000000000010000010001100000d73e030034" +
	"330100010001000100010000000100640065006600610075006c007400000000000000" +
	"0100010001000100010001000106010001000100010001000100010001000100010001" +
	"0001000100010001000100000010b10000000000000000000000000000000000000001"

// * Record holders are known by name only. They are given a PID made from
// * their name, in a range Nintendo did not use, so that the same holder
// * gets the same PID each time
const (
	madeUpPIDBase  = 1_500_000_000
	madeUpPIDRange = 100_000_000
)

func madeUpPID(name string) uint32 {
	hash := fnv.New32a()
	hash.Write([]byte(strings.ToLower(name)))

	return madeUpPIDBase + hash.Sum32()%madeUpPIDRange
}

// * A maker meta binary is usable when it is the size a game stores, says
// * what it is, and holds a Mii whose checksum is right
func usableMakerMetaBinary(meta []byte) bool {
	if (len(meta) != 136 && len(meta) != 140) || !bytes.HasPrefix(meta, []byte("BPFC")) {
		return false
	}

	held := meta[makerMiiOffset : makerMiiOffset+mii.Size]

	return binary.BigEndian.Uint16(held[mii.Size-2:]) == mii.Checksum(held[:mii.Size-2])
}

func (p ArchivedPlayer) makerMetaBinary() []byte {
	if usableMakerMetaBinary(p.MakerMetaBinary) {
		return p.MakerMetaBinary
	}

	fallback, _ := hex.DecodeString(fallbackMakerMetaBinary)

	return MakerMetaBinary(fallback, p.Name, p.PID)
}

func invalid(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidCourse, fmt.Sprintf(format, arguments...))
}

// * Checks everything that can be checked before anything is written
func (c ArchivedCourse) check() ([]byte, error) {
	if c.DataID == 0 {
		return nil, invalid("it has no DataID")
	}

	if c.Maker.PID == 0 || strings.TrimSpace(c.Maker.Name) == "" {
		return nil, invalid("its maker has no PID or no name")
	}

	if !utf8.ValidString(c.Name) || utf8.RuneCountInString(c.Name) > 32 {
		return nil, invalid("its name is not text of at most 32 characters")
	}

	if c.Clears < 0 || c.Attempts < c.Clears || c.Stars < 0 {
		return nil, invalid("its numbers do not add up: %d clears of %d attempts, %d stars", c.Clears, c.Attempts, c.Stars)
	}

	if c.Record != nil && (c.Record.Milliseconds <= 0 || strings.TrimSpace(c.Record.Holder.Name) == "") {
		return nil, invalid("its record has no time or no holder")
	}

	// * This also checks that the file is a course: four parts that
	// * decompress to the sizes a course has
	meta, err := coursefile.MetaBinary(c.File)
	if err != nil {
		return nil, invalid("%v", err)
	}

	return meta, nil
}

// ImportCourse adds a course from an archive of Nintendo's servers, with
// its maker, numbers and record, keeping its DataID. It is checked
// before anything is written, and returns ErrInvalidCourse when it can
// not be used, and ErrAlreadyImported when it is already here
func (s *Seeder) ImportCourse(course ArchivedCourse) error {
	meta, err := course.check()
	if err != nil {
		return err
	}

	if _, nexError := s.Store.GetObjectInfoByDataID(types.UInt64(course.DataID)); nexError == nil {
		return ErrAlreadyImported
	}

	maker, err := s.ensureMaker(course.Maker)
	if err != nil {
		return err
	}

	// * Stored before the course exists, so that a course is never listed
	// * without its file. Storing the same course again writes the same file
	if err := s.PutFile(fmt.Sprintf("%s/%d.bin", s.KeyBase, course.DataID), course.File); err != nil {
		return fmt.Errorf("storing the file of course %d: %w", course.DataID, err)
	}

	param := datastore_types.NewDataStorePreparePostParam()
	param.Size = types.UInt32(len(course.File))
	param.Name = types.NewString(course.Name)
	param.DataType = courseDataType
	param.MetaBinary = types.NewQBuffer(meta)
	param.DelPermission.Permission = deletePermission
	param.Flag = courseFlag
	param.Period = objectPeriod
	param.ExtraData = courseExtraData()
	param.PersistenceInitParam.PersistenceSlotID = noPersistenceSlot

	if err := s.Store.ImportObject(course.DataID, types.PID(course.Maker.PID), param, course.Uploaded); err != nil {
		if errors.Is(err, store.ErrObjectExists) {
			return ErrAlreadyImported
		}

		return err
	}

	if err := s.finishObject(course.DataID, courseRatingSlots); err != nil {
		return err
	}

	dataID := types.UInt64(course.DataID)

	// * Each total is added at once. How many players there were is not
	// * archived, so the players who cleared it stand in, which is the
	// * fewest there can have been
	for slot, total := range map[int]int{
		store.RatingSlotPlays:    course.Clears,
		store.RatingSlotClears:   course.Clears,
		store.RatingSlotAttempts: course.Attempts,
		store.RatingSlotFails:    course.Attempts - course.Clears,
	} {
		if total > 0 {
			if _, nexError := s.Store.RateObjectWithPassword(dataID, types.UInt8(slot), types.Int32(total), 0); nexError != nil {
				return nexError
			}
		}
	}

	if nexError := s.Store.InsertOrUpdateCustomRanking(dataID, starsApplicationID, types.UInt32(course.Stars)); nexError != nil {
		return nexError
	}

	if nexError := s.Store.InsertOrUpdateCustomRanking(maker, makerStarsApplicationID, types.UInt32(course.Stars)); nexError != nil {
		return nexError
	}

	if course.Record != nil && course.Clears > 0 {
		holder := course.Record.Holder
		if holder.PID == 0 {
			holder.PID = madeUpPID(holder.Name)
		}

		if _, err := s.ensureMaker(holder); err != nil {
			return err
		}

		// * Who cleared it first is not archived, so the record holder stands in
		if nexError := s.Store.InsertOrUpdateCourseRecord(dataID, clearRecordSlot, types.PID(holder.PID), types.Int32(course.Record.Milliseconds)); nexError != nil {
			return nexError
		}
	}

	return nil
}

// * Makes a maker object for a player who has none, so that a game can
// * show them. Returns the maker object's DataID
func (s *Seeder) ensureMaker(player ArchivedPlayer) (types.UInt64, error) {
	if existing, nexError := s.makerOf(types.PID(player.PID)); nexError == nil {
		return existing.DataID, nil
	}

	param := datastore_types.NewDataStorePreparePostParam()
	param.Name = types.NewString(player.Name)
	param.DataType = makerDataType
	param.MetaBinary = types.NewQBuffer(player.makerMetaBinary())
	param.DelPermission.Permission = deletePermission
	param.Flag = objectFlag
	param.Period = objectPeriod

	makerID, err := s.addObject(types.PID(player.PID), param, makerRatingSlots)
	if err != nil {
		return 0, fmt.Errorf("maker object of %s: %w", player.Name, err)
	}

	if err := s.addMakerRankings(makerID); err != nil {
		return 0, err
	}

	return types.UInt64(makerID), nil
}
