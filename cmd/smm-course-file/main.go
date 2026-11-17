// Command smm-course-file turns a course between the single file a game
// uploads and a zip of its four parts.
//
//	smm-course-file pack <course.zip> <course.bin>
//	smm-course-file check <course.bin>
package main

import (
	"fmt"
	"os"

	"github.com/PretendoNetwork/super-mario-maker/coursefile"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	switch {
	case len(arguments) == 3 && arguments[0] == "pack":
		archive, err := os.ReadFile(arguments[1])
		if err != nil {
			return err
		}

		course, err := coursefile.FromZip(archive)
		if err != nil {
			return err
		}

		file, err := course.Pack()
		if err != nil {
			return err
		}

		// * Read back before it is kept, so that a file a game could
		// * not read is never written
		if _, err := coursefile.Unpack(file); err != nil {
			return fmt.Errorf("the packed file could not be read back: %w", err)
		}

		return os.WriteFile(arguments[2], file, 0o644)
	case len(arguments) == 2 && arguments[0] == "check":
		file, err := os.ReadFile(arguments[1])
		if err != nil {
			return err
		}

		if _, err := coursefile.Unpack(file); err != nil {
			return err
		}

		fmt.Println("a course file with all four parts")

		return nil
	default:
		return fmt.Errorf("usage: smm-course-file pack <course.zip> <course.bin> | smm-course-file check <course.bin>")
	}
}
