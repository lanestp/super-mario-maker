// Command smm-seed-courses fills a server with made up makers and course
// statistics around the course files in a directory, so that a private
// server has something in Course World to try out.
//
//	smm-seed-courses -mii-from <username> <directory>
//
// The directory holds course files as a game uploads them (see
// smm-course-file). The makers and every number are made up. Files that
// were added before are skipped, as the directory keeps a list of them
// in seeded.json.
//
// It reads the same settings as the server: PN_SMM_DATABASE and the
// settings of that database, and the PN_SMM_CONFIG_S3_ settings
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"

	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/seed"
	"github.com/PretendoNetwork/super-mario-maker/store/open"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const seededListName = "seeded.json"

// * How many courses each made up maker stars
const starsPerMaker = 6

func main() {
	miiFrom := flag.String("mii-from", "", "username of an account that has entered Course World. Its Mii is given to every made up maker")
	makers := flag.Int("makers", 8, "how many made up makers to share the courses between")
	prefix := flag.String("maker-prefix", "SeedMaker", "what the usernames of the made up makers start with")
	randomSeed := flag.Uint64("seed", 1, "the made up numbers are the same for the same seed")
	flag.Parse()

	if *miiFrom == "" || flag.NArg() != 1 || *makers < 1 {
		fmt.Fprintln(os.Stderr, "usage: smm-seed-courses -mii-from <username> [-makers 8] [-seed 1] <directory of course files>")
		os.Exit(2)
	}

	if err := run(flag.Arg(0), *miiFrom, *prefix, *makers, *randomSeed); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(directory, miiFrom, prefix string, makerCount int, randomSeed uint64) error {
	globals.Logger = plogger.NewLogger()

	opened, err := open.FromEnv()
	if err != nil {
		return err
	}
	defer opened.Close()

	putFile, err := filePutter()
	if err != nil {
		return err
	}

	seeded, err := readSeededList(directory)
	if err != nil {
		return err
	}

	names, err := courseFileNames(directory)
	if err != nil {
		return err
	}

	account, err := opened.GetAccountByUsername(miiFrom)
	if err != nil {
		return fmt.Errorf("account %s: %w", miiFrom, err)
	}

	seeder := &seed.Seeder{
		Store:   opened,
		PutFile: putFile,
		KeyBase: os.Getenv("PN_SMM_CONFIG_S3_KEY_BASE"),
		Random:  rand.New(rand.NewPCG(randomSeed, uint64(len(seeded)))),
	}

	makers, err := seeder.Makers(prefix, makerCount, account.PID)
	if err != nil {
		return err
	}

	added := 0

	for _, name := range names {
		if _, done := seeded[name]; done {
			continue
		}

		file, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return err
		}

		owner := makers[seeder.Random.IntN(len(makers))]
		stats := seeder.MakeStats()

		dataID, err := seeder.Course(file, owner, stats)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}

		// * A course that has been cleared has a first clear and a
		// * record, each by one of the made up makers
		if stats.Clears > 0 {
			first := makers[seeder.Random.IntN(len(makers))]
			fastest := makers[seeder.Random.IntN(len(makers))]

			if err := seeder.ClearRecord(dataID, first, fastest); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}

		// * Written after each course, so that stopping part way
		// * through never adds a course twice
		seeded[name] = dataID
		if err := writeSeededList(directory, seeded); err != nil {
			return err
		}

		added++
		fmt.Printf("%s -> course %d by maker %d: %d plays, %d of %d attempts cleared, %d stars\n", name, dataID, owner, stats.Plays, stats.Clears, stats.Attempts, stats.Stars)
	}

	// * Every maker stars a few of the other makers' courses, so that
	// * the list of starred courses on a maker's profile is not empty
	courses := make([]uint64, 0, len(seeded))
	for _, dataID := range seeded {
		courses = append(courses, dataID)
	}

	slices.Sort(courses)

	for _, maker := range makers {
		if _, err := seeder.Star(maker, courses, starsPerMaker); err != nil {
			return err
		}
	}

	fmt.Printf("Added %d courses. %d of the files in %s are on the server\n", added, len(seeded), directory)

	return nil
}

func filePutter() (func(key string, data []byte) error, error) {
	bucket := os.Getenv("PN_SMM_CONFIG_S3_BUCKET")
	if bucket == "" {
		return nil, errors.New("PN_SMM_CONFIG_S3_BUCKET environment variable not set")
	}

	// * As in the server, anything but a clear "false" means TLS
	secure, err := strconv.ParseBool(os.Getenv("PN_SMM_CONFIG_S3_SECURE"))
	if err != nil {
		secure = true
	}

	client, err := minio.New(os.Getenv("PN_SMM_CONFIG_S3_ENDPOINT"), &minio.Options{
		Creds:  credentials.NewStaticV4(os.Getenv("PN_SMM_CONFIG_S3_ACCESS_KEY"), os.Getenv("PN_SMM_CONFIG_S3_ACCESS_SECRET"), ""),
		Secure: secure,
	})
	if err != nil {
		return nil, err
	}

	return func(key string, data []byte) error {
		_, err := client.PutObject(context.Background(), bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{})
		return err
	}, nil
}

func courseFileNames(directory string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(directory, "*.bin"))
	if err != nil {
		return nil, err
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("there are no .bin course files in %s", directory)
	}

	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, filepath.Base(match))
	}

	sort.Strings(names)

	return names, nil
}

func readSeededList(directory string) (map[string]uint64, error) {
	seeded := map[string]uint64{}

	data, err := os.ReadFile(filepath.Join(directory, seededListName))
	if errors.Is(err, fs.ErrNotExist) {
		return seeded, nil
	}

	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(data, &seeded); err != nil {
		return nil, fmt.Errorf("%s: %w", seededListName, err)
	}

	return seeded, nil
}

func writeSeededList(directory string, seeded map[string]uint64) error {
	data, err := json.MarshalIndent(seeded, "", "\t")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(directory, seededListName), data, 0o644)
}
