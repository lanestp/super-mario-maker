// Package coursefile reads and writes the file a Super Mario Maker game
// uploads for a course.
//
// The file is four parts, each compressed with ASH0 and placed one after
// the other with nothing in between: the preview picture, the main area,
// the sub area and the small picture. This was checked against 100 course
// files uploaded by real games to Nintendo's servers, all of which have
// exactly these four parts with exactly these sizes once decompressed
package coursefile

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/PretendoNetwork/super-mario-maker/ash0"
)

const (
	// * The sizes of the parts once decompressed
	PictureSize = 51200
	AreaSize    = 86016

	partCount = 4
)

var ErrNotACourseFile = errors.New("coursefile: not a course file")

// Course holds the parts of a course file, decompressed. A picture starts
// with a 4 byte checksum and a 4 byte length, followed by a JPEG
type Course struct {
	Preview   []byte
	MainArea  []byte
	SubArea   []byte
	Thumbnail []byte
}

func (c *Course) parts() [partCount]*[]byte {
	return [partCount]*[]byte{&c.Preview, &c.MainArea, &c.SubArea, &c.Thumbnail}
}

var (
	partNames = [partCount]string{"preview picture", "main area", "sub area", "small picture"}
	partSizes = [partCount]int{PictureSize, AreaSize, AreaSize, PictureSize}

	// * The names the parts have when a course is saved by a console or
	// * handed out by a course site
	zipNames = [partCount]string{"thumbnail0.tnl", "course_data.cdt", "course_data_sub.cdt", "thumbnail1.tnl"}
)

// Pack returns the file a game would upload for the course
func (c *Course) Pack() ([]byte, error) {
	var file []byte

	for i, part := range c.parts() {
		if len(*part) != partSizes[i] {
			return nil, fmt.Errorf("%w: the %s is %d bytes, want %d", ErrNotACourseFile, partNames[i], len(*part), partSizes[i])
		}

		compressed, err := ash0.Compress(*part)
		if err != nil {
			return nil, err
		}

		file = append(file, compressed...)
	}

	return file, nil
}

// * Finds where each part starts, followed by where the file ends
func partStarts(file []byte) ([]int, error) {
	// * A part does not say how long it is compressed, so the parts are
	// * found by their headers. Each part is a whole number of 4 byte words
	var starts []int

	for offset := 0; offset+8 <= len(file); offset += 4 {
		if bytes.Equal(file[offset:offset+5], []byte("ASH0\x00")) {
			starts = append(starts, offset)
		}
	}

	if len(starts) != partCount || starts[0] != 0 {
		return nil, fmt.Errorf("%w: found %d parts, want %d", ErrNotACourseFile, len(starts), partCount)
	}

	return append(starts, len(file)), nil
}

// MetaBinarySize is the size of the meta binary a game stores with a course
const MetaBinarySize = 44

// MetaBinary returns the meta binary a game stores on the server with a
// course file. The game reads it before the file, to know how long each
// compressed part is and to check that each arrived whole. A course
// stored without it loads with nothing in it, and one with the wrong
// checksums is refused as a broken course.
//
// The layout was read from a course uploaded by a real game, and gives
// the same 44 bytes for that file: eleven big endian 32 bit numbers,
// which are 3, 1, the sizes of the compressed main area, sub area,
// preview picture and small picture, 2, and the CRC-32 of the same four
// compressed parts in the same order.
//
// GUESS. The meaning of the numbers 3, 1 and 2 is not known. They are
// copied as they were seen
func MetaBinary(file []byte) ([]byte, error) {
	if _, err := Unpack(file); err != nil {
		return nil, err
	}

	starts, err := partStarts(file)
	if err != nil {
		return nil, err
	}

	// * The order the meta binary lists the parts in, which is not the
	// * order they have in the file
	order := [partCount]int{1, 2, 0, 3}

	meta := make([]byte, 0, MetaBinarySize)
	meta = binary.BigEndian.AppendUint32(meta, 3)
	meta = binary.BigEndian.AppendUint32(meta, 1)

	for _, part := range order {
		meta = binary.BigEndian.AppendUint32(meta, uint32(starts[part+1]-starts[part]))
	}

	meta = binary.BigEndian.AppendUint32(meta, 2)

	for _, part := range order {
		meta = binary.BigEndian.AppendUint32(meta, crc32.ChecksumIEEE(file[starts[part]:starts[part+1]]))
	}

	return meta, nil
}

// Unpack reads the file a game uploaded for a course
func Unpack(file []byte) (*Course, error) {
	starts, err := partStarts(file)
	if err != nil {
		return nil, err
	}

	course := &Course{}

	for i, part := range course.parts() {
		data, err := ash0.Decompress(file[starts[i]:starts[i+1]])
		if err != nil {
			return nil, fmt.Errorf("%w: the %s: %w", ErrNotACourseFile, partNames[i], err)
		}

		if len(data) != partSizes[i] {
			return nil, fmt.Errorf("%w: the %s is %d bytes, want %d", ErrNotACourseFile, partNames[i], len(data), partSizes[i])
		}

		*part = data
	}

	return course, nil
}

// FromZip reads a course from a zip of its four parts, as saved by a
// console or handed out by a course site
func FromZip(archive []byte) (*Course, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotACourseFile, err)
	}

	course := &Course{}

	for i, part := range course.parts() {
		data, err := readZipFile(reader, zipNames[i], partSizes[i])
		if err != nil {
			return nil, err
		}

		*part = data
	}

	return course, nil
}

func readZipFile(reader *zip.Reader, name string, size int) ([]byte, error) {
	file, err := reader.Open(name)
	if err != nil {
		return nil, fmt.Errorf("%w: the zip has no %s", ErrNotACourseFile, name)
	}
	defer file.Close()

	// * One byte more than wanted is read, so that a file that is too
	// * large is noticed without reading all of it
	data, err := io.ReadAll(io.LimitReader(file, int64(size)+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrNotACourseFile, name, err)
	}

	if len(data) != size {
		return nil, fmt.Errorf("%w: %s is not %d bytes", ErrNotACourseFile, name, size)
	}

	return data, nil
}
