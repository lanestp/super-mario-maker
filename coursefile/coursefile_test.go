package coursefile

import (
	"archive/zip"
	"bytes"
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/PretendoNetwork/super-mario-maker/ash0"
)

// * Parts shaped like real ones: mostly empty areas, and pictures that
// * do not compress
func testCourse() *Course {
	random := rand.New(rand.NewPCG(5, 6))

	fill := func(size int, every int) []byte {
		data := make([]byte, size)
		for i := 0; i < size; i += every {
			data[i] = byte(random.IntN(256))
		}

		return data
	}

	return &Course{
		Preview:   fill(PictureSize, 1),
		MainArea:  fill(AreaSize, 40),
		SubArea:   fill(AreaSize, 90),
		Thumbnail: fill(PictureSize, 2),
	}
}

func sameCourse(a, b *Course) bool {
	return bytes.Equal(a.Preview, b.Preview) && bytes.Equal(a.MainArea, b.MainArea) &&
		bytes.Equal(a.SubArea, b.SubArea) && bytes.Equal(a.Thumbnail, b.Thumbnail)
}

func TestPackThenUnpackGivesTheSameCourse(t *testing.T) {
	course := testCourse()

	file, err := course.Pack()
	if err != nil {
		t.Fatal(err)
	}

	unpacked, err := Unpack(file)
	if err != nil {
		t.Fatal(err)
	}

	if !sameCourse(course, unpacked) {
		t.Error("the unpacked course differs from the one packed")
	}
}

func TestPackPutsThePartsInTheOrderAGameDoes(t *testing.T) {
	course := testCourse()

	file, err := course.Pack()
	if err != nil {
		t.Fatal(err)
	}

	var want []byte
	for _, part := range [][]byte{course.Preview, course.MainArea, course.SubArea, course.Thumbnail} {
		compressed, err := ash0.Compress(part)
		if err != nil {
			t.Fatal(err)
		}

		want = append(want, compressed...)
	}

	if !bytes.Equal(file, want) {
		t.Error("the file is not preview, main area, sub area, small picture, one after the other")
	}
}

func TestPackRefusesAPartOfTheWrongSize(t *testing.T) {
	course := testCourse()
	course.SubArea = course.SubArea[:AreaSize-1]

	if _, err := course.Pack(); !errors.Is(err, ErrNotACourseFile) {
		t.Errorf("got %v, want ErrNotACourseFile", err)
	}
}

func TestUnpackRefusesWhatIsNotACourseFile(t *testing.T) {
	file, err := testCourse().Pack()
	if err != nil {
		t.Fatal(err)
	}

	onePart, err := ash0.Compress(make([]byte, PictureSize))
	if err != nil {
		t.Fatal(err)
	}

	swapped := testCourse()
	swapped.Preview, swapped.MainArea = swapped.MainArea, swapped.Preview

	var wrongOrder []byte
	for _, part := range [][]byte{swapped.Preview, swapped.MainArea, swapped.SubArea, swapped.Thumbnail} {
		compressed, _ := ash0.Compress(part)
		wrongOrder = append(wrongOrder, compressed...)
	}

	for name, bad := range map[string][]byte{
		"nothing":            nil,
		"not compressed":     bytes.Repeat([]byte("course"), 1000),
		"one part":           onePart,
		"five parts":         append(bytes.Clone(file), onePart...),
		"header before":      append([]byte{0, 0, 0, 0}, file...),
		"cut short":          file[:len(file)-2000],
		"parts out of order": wrongOrder,
	} {
		if _, err := Unpack(bad); !errors.Is(err, ErrNotACourseFile) {
			t.Errorf("%s: got %v, want ErrNotACourseFile", name, err)
		}
	}
}

func makeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()

	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)

	for name, data := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	return archive.Bytes()
}

func TestFromZipReadsEachPartFromItsFile(t *testing.T) {
	course := testCourse()

	read, err := FromZip(makeZip(t, map[string][]byte{
		"thumbnail0.tnl":      course.Preview,
		"course_data.cdt":     course.MainArea,
		"course_data_sub.cdt": course.SubArea,
		"thumbnail1.tnl":      course.Thumbnail,
	}))
	if err != nil {
		t.Fatal(err)
	}

	if !sameCourse(course, read) {
		t.Error("a part was read from the wrong file")
	}
}

func TestFromZipRefusesAZipThatIsNotACourse(t *testing.T) {
	course := testCourse()

	for name, files := range map[string]map[string][]byte{
		"file missing": {
			"thumbnail0.tnl":  course.Preview,
			"course_data.cdt": course.MainArea,
			"thumbnail1.tnl":  course.Thumbnail,
		},
		"file too large": {
			"thumbnail0.tnl":      course.Preview,
			"course_data.cdt":     append(bytes.Clone(course.MainArea), 0),
			"course_data_sub.cdt": course.SubArea,
			"thumbnail1.tnl":      course.Thumbnail,
		},
	} {
		if _, err := FromZip(makeZip(t, files)); !errors.Is(err, ErrNotACourseFile) {
			t.Errorf("%s: got %v, want ErrNotACourseFile", name, err)
		}
	}

	if _, err := FromZip([]byte("not a zip")); !errors.Is(err, ErrNotACourseFile) {
		t.Errorf("not a zip: got %v, want ErrNotACourseFile", err)
	}
}
