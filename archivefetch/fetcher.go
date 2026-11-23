// Package archivefetch brings courses in from the archive of Nintendo's
// Super Mario Maker servers while the server runs, so that every archived
// course can be played and not only the ones imported ahead of time.
//
// Two things use it. A player who asks for a course the server does not
// have (by its course ID) waits a few seconds while it is fetched. And a
// background fill keeps bringing in random courses from the whole
// archive, so that the random lists the game shows reach past what was
// imported. Either way a course is fetched once: its file goes to the
// server's storage and it is a normal course from then on.
//
// A course is only ever listed once it is here, so the game is never
// handed a course it can not download.
package archivefetch

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/super-mario-maker/seed"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

const (
	// IndexURL is the community index of the archive
	IndexURL = "https://api.bobac-analytics.com/smm1/"
	// WaybackURL asks for a file as it was. "id_" leaves it unwrapped,
	// and the short timestamp is sent on to the copy nearest to it
	WaybackURL = "https://web.archive.org/web/2id_/"

	// * The first and last positions in the index that hold a course
	firstPosition = 7_515_625
	lastPosition  = 18_112_792

	// * How many courses the background fill has brought in is kept
	// * under this name, with the import runs' positions
	fillCounter = "archive-fill-count"
	// * How far down the catalog's most played courses the fill has got
	fillRank = "archive-fill-rank"

	// * How long a fetch a player started may take, once nobody is
	// * waiting on it any more
	fetchTimeout = 2 * time.Minute

	maxRemembered = 100_000
)

// * How long a course that could not be fetched is not asked for again
var missFor = map[string]time.Duration{
	store.ImportNotArchived: 24 * time.Hour,
	store.ImportRefused:     7 * 24 * time.Hour,
	store.ImportFailed:      5 * time.Minute,
}

// Config is what a Fetcher needs
type Config struct {
	Seeder *seed.Seeder
	// Catalog, when given, is where everything about a course comes
	// from, and the fill goes through its courses most played first. The
	// archive's index is then only asked where a course's file is
	Catalog *Catalog
	// FillLimit is how many courses the background fill brings in before
	// it stops. 0 turns it off
	FillLimit int64
	// FillGap is the least time between two of the fill's requests to
	// the archive, and UrgentGap between two made for a waiting player
	FillGap   time.Duration
	UrgentGap time.Duration
	Log       func(string)

	// * Tests point these somewhere else
	HTTP       *http.Client
	Index      string
	Wayback    string
	Positions  [2]int64
	FillPause  time.Duration
	RandomInt  func(n int64) int64
	RetryAfter func(outcome string) time.Duration
}

// Fetcher brings courses in from the archive
type Fetcher struct {
	config Config
	client *client

	// * Imports are one at a time, as the seeder is not safe to share
	importing sync.Mutex

	mutex    sync.Mutex
	inFlight map[uint64]*fetch
	missed   map[uint64]time.Time
}

type fetch struct {
	done chan struct{}
	ok   bool
}

// New makes a Fetcher
func New(config Config) (*Fetcher, error) {
	if config.Seeder == nil || config.Seeder.Store == nil {
		return nil, errors.New("archivefetch: a seeder with a store is needed")
	}

	if config.Log == nil {
		config.Log = func(string) {}
	}

	if config.HTTP == nil {
		config.HTTP = &http.Client{Timeout: 30 * time.Second}
	}

	if config.Index == "" {
		config.Index = IndexURL
	}

	if config.Wayback == "" {
		config.Wayback = WaybackURL
	}

	if config.Positions == [2]int64{} {
		config.Positions = [2]int64{firstPosition, lastPosition}
	}

	if config.FillPause == 0 {
		config.FillPause = time.Hour
	}

	if config.RandomInt == nil {
		config.RandomInt = rand.Int64N
	}

	if config.RetryAfter == nil {
		config.RetryAfter = func(outcome string) time.Duration { return missFor[outcome] }
	}

	return &Fetcher{
		config: config,
		client: &client{
			http:    config.HTTP,
			index:   config.Index,
			wayback: config.Wayback,
			gate:    &gate{fillGap: config.FillGap, urgentGap: config.UrgentGap},
		},
		inFlight: map[uint64]*fetch{},
		missed:   map[uint64]time.Time{},
	}, nil
}

// Fetch brings in a course a player asked for, if the archive has it,
// and says whether the server now has it. It waits until ctx ends at the
// most; a fetch that is not done by then carries on, so that the course
// is here the next time it is asked for
func (f *Fetcher) Fetch(ctx context.Context, dataID uint64) bool {
	// * Only an ID an archived course could have. The server's own
	// * objects are given IDs outside these
	if dataID < store.FirstDataID || dataID >= store.ImportedDataIDLimit {
		return false
	}

	if f.here(dataID) {
		return true
	}

	f.mutex.Lock()

	if until, ok := f.missed[dataID]; ok && time.Now().Before(until) {
		f.mutex.Unlock()
		return false
	}

	current, ok := f.inFlight[dataID]
	if !ok {
		current = &fetch{done: make(chan struct{})}
		f.inFlight[dataID] = current

		go f.run(dataID, current)
	}

	f.mutex.Unlock()

	select {
	case <-current.done:
		return current.ok
	case <-ctx.Done():
		return false
	}
}

func (f *Fetcher) run(dataID uint64, current *fetch) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	outcome, detail := f.bringIn(ctx, dataID)

	f.mutex.Lock()

	delete(f.inFlight, dataID)

	if outcome != "" {
		if len(f.missed) >= maxRemembered {
			clear(f.missed)
		}

		f.missed[dataID] = time.Now().Add(f.config.RetryAfter(outcome))
	}

	f.mutex.Unlock()

	if outcome == "" {
		f.config.Log(fmt.Sprintf("Brought in course %d from the archive, as a player asked for it", dataID))
	} else if outcome != store.ImportNotArchived {
		f.config.Log(fmt.Sprintf("Course %d could not be brought in from the archive (%s): %s", dataID, outcome, detail))
	}

	current.ok = outcome == ""
	close(current.done)
}

// * Answers no outcome when the course is here
func (f *Fetcher) bringIn(ctx context.Context, dataID uint64) (string, string) {
	entry, err := f.entry(ctx, dataID, true)
	if err != nil {
		return store.ImportFailed, err.Error()
	}

	if entry == nil {
		// * Not a course the index knows. Nothing is kept about it, as
		// * most of these are IDs that were never a course
		return store.ImportNotArchived, "the index has no course with this ID"
	}

	return f.bringInEntry(ctx, *entry, true)
}

// * What is known about a course, with the address of its file: all of
// * it from the index, or, with a catalog, everything but the address
// * from the catalog. nil when neither has the course
func (f *Fetcher) entry(ctx context.Context, dataID uint64, urgent bool) (*Entry, error) {
	if f.config.Catalog == nil {
		return f.client.byID(ctx, dataID, urgent)
	}

	entry, err := f.config.Catalog.Course(dataID)
	if err != nil || entry == nil {
		return nil, err
	}

	indexed, err := f.client.byID(ctx, dataID, urgent)
	if err != nil {
		return nil, err
	}

	if indexed == nil {
		// * The index has no address for its file, which tryEntry turns
		// * into a refusal
		return entry, nil
	}

	entry.URL = indexed.URL

	return entry, nil
}

// * Answers no outcome when the course is here. A course that could not
// * be brought in is kept track of, as the import tool does, so that
// * smm-import-archive -retry can try it again
func (f *Fetcher) bringInEntry(ctx context.Context, entry Entry, urgent bool) (string, string) {
	outcome, detail := f.tryEntry(ctx, entry, urgent)

	if outcome != "" && ctx.Err() == nil {
		attempt := store.ImportAttempt{DataID: entry.LevelID, Outcome: outcome, Detail: detail}

		if encoded, err := jsonOf(entry); err == nil {
			attempt.Entry = encoded
		}

		if err := f.config.Seeder.Store.RecordImportAttempt(attempt); err != nil {
			f.config.Log(fmt.Sprintf("The attempt at course %d could not be kept: %v", entry.LevelID, err))
		}
	}

	return outcome, detail
}

func (f *Fetcher) tryEntry(ctx context.Context, entry Entry, urgent bool) (string, string) {
	if entry.URL == "" {
		return store.ImportRefused, "the index has no address for its file"
	}

	if _, err := entry.Archived(nil); err != nil {
		return store.ImportRefused, "the index entry can not be used: " + err.Error()
	}

	file, err := f.client.file(ctx, entry.URL, urgent)
	if err != nil {
		return store.ImportFailed, err.Error()
	}

	if file == nil {
		return store.ImportNotArchived, "the Wayback Machine has no copy of the file"
	}

	course, _ := entry.Archived(file)

	f.importing.Lock()
	err = f.config.Seeder.ImportCourse(course)
	f.importing.Unlock()

	switch {
	case err == nil, errors.Is(err, seed.ErrAlreadyImported):
		if forgetErr := f.config.Seeder.Store.ForgetImportAttempt(entry.LevelID); forgetErr != nil {
			f.config.Log(fmt.Sprintf("The attempts at course %d could not be forgotten: %v", entry.LevelID, forgetErr))
		}

		return "", ""
	case errors.Is(err, seed.ErrInvalidCourse):
		return store.ImportRefused, err.Error()
	default:
		return store.ImportFailed, err.Error()
	}
}

func (f *Fetcher) here(dataID uint64) bool {
	_, nexError := f.config.Seeder.Store.GetObjectInfoByDataID(types.UInt64(dataID))
	return nexError == nil
}

// Fill brings in random courses from the whole archive, one at a time,
// until it has brought in FillLimit of them or ctx ends. It carries on
// from its count after a restart. A player waiting on a course always
// goes ahead of it
func (f *Fetcher) Fill(ctx context.Context) {
	if f.config.FillLimit <= 0 {
		return
	}

	if f.config.Catalog != nil {
		f.fillFromCatalog(ctx)
		return
	}

	kept := f.config.Seeder.Store

	for ctx.Err() == nil {
		count, err := kept.ImportCursor(fillCounter)
		if err != nil {
			f.config.Log(fmt.Sprintf("The archive fill's count could not be read: %v", err))
			f.pause(ctx)

			continue
		}

		if count >= f.config.FillLimit {
			f.config.Log(fmt.Sprintf("The archive fill has brought in %d courses, its limit. It stops here", count))
			return
		}

		first, last := f.config.Positions[0], f.config.Positions[1]

		entries, err := f.client.atPosition(ctx, first+f.config.RandomInt(last-first+1))
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if ctx.Err() != nil || entry.LevelID == 0 || f.here(entry.LevelID) {
				continue
			}

			if outcome, _ := f.bringInEntry(ctx, entry, false); outcome != "" {
				continue
			}

			count++

			if err := kept.SetImportCursor(fillCounter, count); err != nil {
				f.config.Log(fmt.Sprintf("The archive fill's count could not be kept: %v", err))
			}

			if count%100 == 0 {
				f.config.Log(fmt.Sprintf("The archive fill has brought in %d courses", count))
			}
		}
	}
}

// * How many courses are read from the catalog at once. Each read sorts
// * the whole catalog, which takes a few seconds
const catalogBatch = 200

// * Goes through the catalog's courses most played first, so the courses
// * players liked most are brought in first. How far it has got is kept,
// * so it carries on from there after a restart
func (f *Fetcher) fillFromCatalog(ctx context.Context) {
	kept := f.config.Seeder.Store

	for ctx.Err() == nil {
		count, countErr := kept.ImportCursor(fillCounter)
		rank, rankErr := kept.ImportCursor(fillRank)

		if countErr != nil || rankErr != nil {
			f.config.Log(fmt.Sprintf("The archive fill's place could not be read: %v", errors.Join(countErr, rankErr)))
			f.pause(ctx)

			continue
		}

		if count >= f.config.FillLimit {
			f.config.Log(fmt.Sprintf("The archive fill has brought in %d courses, its limit. It stops here", count))
			return
		}

		ids, err := f.config.Catalog.MostPlayed(rank, catalogBatch)
		if err != nil {
			f.config.Log(fmt.Sprintf("The catalog could not be read: %v", err))
			f.pause(ctx)

			continue
		}

		if len(ids) == 0 {
			f.config.Log("The archive fill has been through every course in the catalog. It stops here")
			return
		}

		for i := 0; i < len(ids); i++ {
			id := ids[i]

			if ctx.Err() != nil {
				return
			}

			brought := false

			// * A failure that may go right another time, such as the
			// * archive being down, is tried again before moving on. The
			// * gate spaces the tries out further each time
			for try := 0; try < maxFillTries && ctx.Err() == nil && !f.here(id); try++ {
				var again bool
				if brought, again = f.fillOne(ctx, id); !again {
					break
				}
			}

			if ctx.Err() != nil {
				return
			}

			if brought {
				count++

				if err := kept.SetImportCursor(fillCounter, count); err != nil {
					f.config.Log(fmt.Sprintf("The archive fill's count could not be kept: %v", err))
				}

				if count%100 == 0 {
					f.config.Log(fmt.Sprintf("The archive fill has brought in %d courses, and is %d courses down the most played", count, rank))
				}

				if count >= f.config.FillLimit {
					break
				}
			}

			rank++

			if err := kept.SetImportCursor(fillRank, rank); err != nil {
				f.config.Log(fmt.Sprintf("The archive fill's place could not be kept: %v", err))
			}
		}
	}
}

// * How many times the fill tries a course that failed in a way that may
// * go right another time
const maxFillTries = 5

// * Brings in one course for the fill. Says whether it is here now, and
// * whether it is worth trying again
func (f *Fetcher) fillOne(ctx context.Context, dataID uint64) (brought, again bool) {
	entry, err := f.entry(ctx, dataID, false)
	if err != nil {
		return false, true
	}

	if entry == nil {
		return false, false
	}

	outcome, _ := f.bringInEntry(ctx, *entry, false)

	return outcome == "", outcome == store.ImportFailed
}

func (f *Fetcher) pause(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(f.config.FillPause):
	}
}
