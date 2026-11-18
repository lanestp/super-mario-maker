package mii_test

import (
	"bytes"
	"encoding/binary"
	"testing"
	"unicode/utf16"

	"github.com/PretendoNetwork/super-mario-maker/mii"
)

// * The check value CRC-16/XMODEM is published with, for the ASCII
// * bytes 123456789 (CRC RevEng catalogue)
func TestChecksumIsCRC16XMODEM(t *testing.T) {
	if got := mii.Checksum([]byte("123456789")); got != 0x31C3 {
		t.Errorf("checksum of 123456789 = %#04x, want 0x31c3", got)
	}

	if got := mii.Checksum(nil); got != 0 {
		t.Errorf("checksum of nothing = %#04x, want 0", got)
	}
}

func TestChecksumOfAMiiWithItsChecksumIsZero(t *testing.T) {
	data := bytes.Repeat([]byte{0xA5, 0x3C, 0x00, 0xFF}, 24)[:mii.Size-2]
	whole := binary.BigEndian.AppendUint16(append([]byte{}, data...), mii.Checksum(data))

	if got := mii.Checksum(whole); got != 0 {
		t.Errorf("checksum of a Mii with its checksum in place = %#04x, want 0", got)
	}
}

func TestRenamedChangesOnlyTheNameIDAndChecksum(t *testing.T) {
	original := make([]byte, mii.Size)
	for i := range original {
		original[i] = byte(i * 7)
	}

	before := append([]byte{}, original...)
	renamed := mii.Renamed(original, "Mario", 1712345678)

	if !bytes.Equal(original, before) {
		t.Fatal("Renamed changed the Mii it was given")
	}

	if len(renamed) != mii.Size {
		t.Fatalf("renamed Mii is %d bytes, want %d", len(renamed), mii.Size)
	}

	if id := binary.LittleEndian.Uint32(renamed[0x8:]); id != 1712345678 {
		t.Errorf("ID = %d, want 1712345678", id)
	}

	name := make([]uint16, mii.NameLength)
	for i := range name {
		name[i] = binary.LittleEndian.Uint16(renamed[0x1A+i*2:])
	}

	if got := string(utf16.Decode(name[:5])); got != "Mario" || name[5] != 0 || name[9] != 0 {
		t.Errorf("name units = %v, want Mario followed by zeros", name)
	}

	if mii.Checksum(renamed) != 0 {
		t.Error("the renamed Mii's checksum does not check out")
	}

	for i := range mii.Size - 2 {
		inID := i >= 0x8 && i < 0xC
		inName := i >= 0x1A && i < 0x1A+mii.NameLength*2

		if !inID && !inName && renamed[i] != original[i] {
			t.Errorf("byte %#x changed from %#x to %#x", i, original[i], renamed[i])
		}
	}
}

func TestRenamedCutsALongNameAtTenUnits(t *testing.T) {
	renamed := mii.Renamed(make([]byte, mii.Size), "ElevenChars", 1)

	name := make([]uint16, mii.NameLength)
	for i := range name {
		name[i] = binary.LittleEndian.Uint16(renamed[0x1A+i*2:])
	}

	if got := string(utf16.Decode(name)); got != "ElevenChar" {
		t.Errorf("name = %q, want ElevenChar", got)
	}
}

func TestRenamedLeavesWhatIsNotAMiiAlone(t *testing.T) {
	for _, size := range []int{0, mii.Size - 1, mii.Size + 1} {
		original := bytes.Repeat([]byte{1}, size)

		if renamed := mii.Renamed(original, "Mario", 1); !bytes.Equal(renamed, original) {
			t.Errorf("a %d byte input was changed", size)
		}
	}
}
