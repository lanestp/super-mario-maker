// Package mii reads and changes the Mii data of the Wii U: the 96 bytes
// that describe a player's character, kept in a console's account and in
// the maker object of each player on the game server
package mii

import (
	"encoding/binary"
	"unicode/utf16"
)

const (
	// Size is the size of a Mii
	Size = 96

	// * Where things are in a Mii, as read from ones a real game stored
	idOffset   = 0x8
	nameOffset = 0x1A

	// NameLength is how many UTF-16 characters a Mii name holds
	NameLength = 10
)

// Renamed returns a copy of a Mii with another name and ID, and the
// checksum redone so that a game takes it. Names longer than NameLength
// are cut short. Anything that is not Size bytes is returned as it is
func Renamed(original []byte, name string, id uint32) []byte {
	renamed := append([]byte{}, original...)
	if len(renamed) != Size {
		return renamed
	}

	units := utf16.Encode([]rune(name))
	units = units[:min(len(units), NameLength)]

	for i := range NameLength {
		var unit uint16
		if i < len(units) {
			unit = units[i]
		}

		binary.LittleEndian.PutUint16(renamed[nameOffset+i*2:], unit)
	}

	binary.LittleEndian.PutUint32(renamed[idOffset:], id)
	binary.BigEndian.PutUint16(renamed[Size-2:], Checksum(renamed[:Size-2]))

	return renamed
}

// Checksum is the checksum kept in the last two bytes of a Mii. With it
// in place, the checksum of the whole Mii is zero
func Checksum(data []byte) uint16 {
	var checksum uint16

	for _, value := range data {
		for bit := 7; bit >= 0; bit-- {
			carry := checksum&0x8000 != 0
			checksum = checksum<<1 | uint16(value>>bit&1)

			if carry {
				checksum ^= 0x1021
			}
		}
	}

	// * Two zero bytes are run through as well, standing in for the
	// * checksum itself
	for range 16 {
		carry := checksum&0x8000 != 0
		checksum <<= 1

		if carry {
			checksum ^= 0x1021
		}
	}

	return checksum
}
