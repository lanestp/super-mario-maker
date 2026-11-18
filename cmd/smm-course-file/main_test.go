package main

import (
	"archive/zip"
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PretendoNetwork/super-mario-maker/coursefile"
)

// * The parts of a course as a console saves them, each filled with a
// * byte of its own so that a mixed up part is noticed
var parts = []struct {
	name string
	size int
	fill byte
}{
	{"thumbnail0.tnl", coursefile.PictureSize, 1},
	{"course_data.cdt", coursefile.AreaSize, 2},
	{"course_data_sub.cdt", coursefile.AreaSize, 3},
	{"thumbnail1.tnl", coursefile.PictureSize, 4},
}

func writeZip(t *testing.T, leaveOut string) string {
	t.Helper()

	var archive bytes.Buffer

	writer := zip.NewWriter(&archive)

	for _, part := range parts {
		if part.name == leaveOut {
			continue
		}

		file, err := writer.Create(part.name)
		if err != nil {
			t.Fatal(err)
		}

		file.Write(bytes.Repeat([]byte{part.fill}, part.size))
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "course.zip")
	if err := os.WriteFile(path, archive.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

func runPrinting(t *testing.T, args ...string) (string, error) {
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

	runError := run(args)

	os.Stdout = previous
	writer.Close()

	return <-printed, runError
}

func TestTheCourseFileCommandPacksAZipIntoTheFileAGameUploads(t *testing.T) {
	packed := filepath.Join(t.TempDir(), "course.bin")

	if _, err := runPrinting(t, "pack", writeZip(t, ""), packed); err != nil {
		t.Fatal(err)
	}

	file, err := os.ReadFile(packed)
	if err != nil {
		t.Fatal(err)
	}

	course, err := coursefile.Unpack(file)
	if err != nil {
		t.Fatal(err)
	}

	for i, part := range [][]byte{course.Preview, course.MainArea, course.SubArea, course.Thumbnail} {
		if !bytes.Equal(part, bytes.Repeat([]byte{parts[i].fill}, parts[i].size)) {
			t.Errorf("%s did not come back as it went in", parts[i].name)
		}
	}
}

func TestTheCourseFileCommandRefusesAZipWithoutAllFourParts(t *testing.T) {
	packed := filepath.Join(t.TempDir(), "course.bin")

	_, err := runPrinting(t, "pack", writeZip(t, "course_data_sub.cdt"), packed)
	if !errors.Is(err, coursefile.ErrNotACourseFile) || !strings.Contains(err.Error(), "the zip has no course_data_sub.cdt") {
		t.Errorf("got %v, want the missing part named", err)
	}

	if _, err := os.Stat(packed); !errors.Is(err, os.ErrNotExist) {
		t.Error("a file was written from a zip that is not a course")
	}

	if _, err := runPrinting(t, "pack", filepath.Join(t.TempDir(), "none.zip"), packed); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a zip that is not there gave %v", err)
	}
}

func packedCourse(t *testing.T) string {
	t.Helper()

	packed := filepath.Join(t.TempDir(), "course.bin")

	if _, err := runPrinting(t, "pack", writeZip(t, ""), packed); err != nil {
		t.Fatal(err)
	}

	return packed
}

func TestTheCourseFileCommandChecksACourseFile(t *testing.T) {
	packed := packedCourse(t)

	printed, err := runPrinting(t, "check", packed)
	if err != nil || printed != "a course file with all four parts\n" {
		t.Errorf("printed %q (%v)", printed, err)
	}

	notACourse := filepath.Join(t.TempDir(), "notes.bin")
	os.WriteFile(notACourse, []byte("not a course"), 0o644)

	printed, err = runPrinting(t, "check", notACourse)
	if !errors.Is(err, coursefile.ErrNotACourseFile) || printed != "" {
		t.Errorf("a file that is not a course printed %q and gave %v", printed, err)
	}

	if _, err := runPrinting(t, "check", filepath.Join(t.TempDir(), "none.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a file that is not there gave %v", err)
	}
}

func TestTheCourseFileCommandPrintsTheMetaBinaryInHexadecimal(t *testing.T) {
	packed := packedCourse(t)

	printed, err := runPrinting(t, "meta", packed)
	if err != nil {
		t.Fatal(err)
	}

	file, _ := os.ReadFile(packed)
	meta, _ := coursefile.MetaBinary(file)

	if printed != hex.EncodeToString(meta)+"\n" || len(meta) != coursefile.MetaBinarySize {
		t.Errorf("printed %q, want the %d byte meta binary in hexadecimal", printed, coursefile.MetaBinarySize)
	}

	notACourse := filepath.Join(t.TempDir(), "notes.bin")
	os.WriteFile(notACourse, []byte("not a course"), 0o644)

	if _, err := runPrinting(t, "meta", notACourse); !errors.Is(err, coursefile.ErrNotACourseFile) {
		t.Errorf("a file that is not a course gave %v", err)
	}

	if _, err := runPrinting(t, "meta", filepath.Join(t.TempDir(), "none.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a file that is not there gave %v", err)
	}
}

func TestTheCourseFileCommandShowsHowToUseItWhenGivenTheWrongArguments(t *testing.T) {
	for _, args := range [][]string{{}, {"pack", "course.zip"}, {"check"}, {"meta", "a", "b"}, {"unpack", "course.bin"}} {
		_, err := runPrinting(t, args...)
		if err == nil || err.Error() != "usage: smm-course-file pack <course.zip> <course.bin> | check <course.bin> | meta <course.bin>" {
			t.Errorf("%q gave %v, want the usage", args, err)
		}
	}
}
