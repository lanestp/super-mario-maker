// Command smm-import-archive brings courses from the archive of Nintendo's
// Super Mario Maker servers into a server.
//
// A public index of the archive lists every course in order, by position.
// This walks the positions in order, fetches each course file from the
// Wayback Machine, and sends it with what the index says about it to the
// server's import service, which checks it and writes it. Nothing is kept
// on disk. How far a run has got is kept by the server, so a run stopped
// part way carries on from there.
//
//	smm-import-archive -step 1000              an even sample of the whole archive
//	smm-import-archive                         every course, in order
//	smm-import-archive -retry                  try again the courses that failed
//
// Courses that are not brought in are kept track of by the server, with
// why, for -retry. PN_SMM_IMPORT_TOKEN is the import service's token, and
// PN_SMM_IMPORT_ADDRESS where it is (127.0.0.1:8384 when not set). It
// waits between every request to the archive, and longer when told to slow
// down, so it brings in a few hundred courses an hour
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/PretendoNetwork/super-mario-maker/importsvc"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

const (
	// * Answers with the course at or after a position. Despite the name,
	// * the same position always gives the same course
	indexURL = "https://api.bobac-analytics.com/smm1/searchRandomLevels/"

	// * The first and last positions that hold a course, found by asking
	firstPosition = 7_515_625
	lastPosition  = 18_112_792

	// * "id_" asks for a file as it was, and not wrapped in a page, and the
	// * short timestamp is sent on to the copy nearest to it
	waybackURL = "https://web.archive.org/web/2id_/"

	agent = "smm-private-server-import/1.0 (one course at a time)"
)

// * What the index says about a course
type indexEntry struct {
	URL             string `json:"url"`
	Name            string `json:"name"`
	Creator         string `json:"creator"`
	LevelID         uint64 `json:"levelid"`
	CreatorID       uint32 `json:"creatorid"`
	Clears          int    `json:"clears"`
	Failures        int    `json:"failures"`
	TotalAttempts   int    `json:"total_attempts"`
	UploadTime      string `json:"uploadTime"`
	RecordMS        int    `json:"world_record_ms"`
	RecordHolder    string `json:"world_record_holder_nnid"`
	RecordHolderMii string `json:"world_record_best_time_player_mii_data"`
	CreatorMii      string `json:"creator_mii_data"`
	Stars           int    `json:"stars"`
}

type importer struct {
	client *http.Client
	// * Where the index and the Wayback Machine are, which tests change
	index   string
	wayback string
	service string
	token   string
	wait    time.Duration
	backoff time.Duration

	imported, alreadyThere, notArchived, refused, failed int
}

func main() {
	from := flag.Int64("from", firstPosition, "the first position in the index to bring in")
	to := flag.Int64("to", lastPosition, "the last position in the index to bring in")
	step := flag.Int64("step", 1, "how far apart the positions brought in are. 1000 brings in an even sample of a thousandth")
	count := flag.Int("count", 0, "stop after bringing in this many courses. 0 goes on to the last position")
	name := flag.String("name", "", "the name the server keeps the run's position under. Made from -from, -to and -step when left out")
	retry := flag.Bool("retry", false, "try again the courses that failed or were not archived, instead of walking the index")
	maxTries := flag.Int("max-tries", 3, "with -retry, leave out courses tried this many times")
	wait := flag.Duration("wait", 3*time.Second, "how long to wait between requests to the archive")
	flag.Parse()

	token := os.Getenv("PN_SMM_IMPORT_TOKEN")
	if token == "" {
		log.Fatal("PN_SMM_IMPORT_TOKEN is not set")
	}

	address := os.Getenv("PN_SMM_IMPORT_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8384"
	}

	if *step < 1 || *from > *to {
		log.Fatal("-step must be 1 or more, and -from no more than -to")
	}

	if *name == "" {
		*name = fmt.Sprintf("positions-%d-%d-%d", *from, *to, *step)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	i := &importer{
		client:  &http.Client{Timeout: time.Minute},
		index:   indexURL,
		wayback: waybackURL,
		service: "http://" + address,
		token:   token,
		wait:    *wait,
	}

	if *retry {
		i.retry(ctx, *maxTries, *count)
		return
	}

	i.walk(ctx, *name, *from, *to, *step, *count)
}

// * Walks the index from where the run got to, saving how far it has got
// * after each position
func (i *importer) walk(ctx context.Context, name string, from, to, step int64, count int) {
	started := time.Now()

	position, err := i.cursor(ctx, name)
	if err != nil {
		log.Fatalf("the run's position could not be read: %v", err)
	}

	if position < from {
		position = from
	}

	log.Printf("run %s starting at position %d of %d to %d, every %d", name, position, from, to, step)

	failuresInARow := 0

	for ; position <= to && (count == 0 || i.imported < count) && ctx.Err() == nil; position += step {
		entries, err := i.atPosition(ctx, position)
		if err != nil {
			if ctx.Err() != nil {
				break
			}

			failuresInARow++
			log.Printf("position %d: the index could not be read: %v", position, err)

			if failuresInARow >= 20 {
				log.Print("stopping after 20 failures in a row. Running it again carries on from here")
				break
			}

			// * The position is tried again, after waiting
			position -= step

			continue
		}

		failuresInARow = 0

		for _, entry := range entries {
			i.bringIn(ctx, entry)
		}

		if ctx.Err() != nil {
			break
		}

		if err := i.setCursor(ctx, name, position+step); err != nil {
			log.Printf("the run's position could not be saved: %v", err)
		}

		if i.imported > 0 && i.imported%25 == 0 {
			i.report(started, position)
		}
	}

	i.report(started, position)
}

// * Tries again the courses the server has kept as failed or not archived
func (i *importer) retry(ctx context.Context, maxTries, count int) {
	started := time.Now()

	limit := count
	if limit == 0 {
		limit = 10000
	}

	attempts, err := i.attemptsToRetry(ctx, maxTries, limit)
	if err != nil {
		log.Fatalf("the courses to try again could not be listed: %v", err)
	}

	log.Printf("trying %d courses again", len(attempts))

	for _, attempt := range attempts {
		if ctx.Err() != nil {
			break
		}

		var entry indexEntry
		if err := json.Unmarshal(attempt.Entry, &entry); err != nil || entry.LevelID == 0 {
			log.Printf("course %d: its index entry could not be read", attempt.DataID)
			continue
		}

		i.bringIn(ctx, entry)
	}

	i.report(started, 0)
}

func (i *importer) report(started time.Time, position int64) {
	log.Printf("%d brought in, %d already there, %d not archived, %d refused, %d failed, after %s, at position %d",
		i.imported, i.alreadyThere, i.notArchived, i.refused, i.failed, time.Since(started).Round(time.Minute), position)
}

func (i *importer) atPosition(ctx context.Context, position int64) ([]indexEntry, error) {
	body, err := i.fetch(ctx, i.index+strconv.FormatInt(position, 10))
	if err != nil {
		return nil, err
	}

	// * A position with no course answers with an error object, not a list
	var entries []indexEntry
	if json.Unmarshal(body, &entries) != nil {
		return nil, nil
	}

	return entries, nil
}

// * Brings one course in, keeping track with the server of a course that
// * could not be brought in
func (i *importer) bringIn(ctx context.Context, entry indexEntry) {
	if entry.LevelID == 0 {
		return
	}

	outcome, detail := i.tryToBringIn(ctx, entry)

	switch outcome {
	case "":
		return
	case store.ImportFailed:
		i.failed++
	case store.ImportNotArchived:
		i.notArchived++
	case store.ImportRefused:
		i.refused++
	}

	if outcome != store.ImportNotArchived {
		log.Printf("course %d %s: %s", entry.LevelID, outcome, detail)
	}

	if ctx.Err() != nil {
		return
	}

	if err := i.recordAttempt(ctx, entry, outcome, detail); err != nil {
		log.Printf("course %d: the attempt could not be recorded: %v", entry.LevelID, err)
	}
}

// * Returns no outcome when the course is here, or was already
func (i *importer) tryToBringIn(ctx context.Context, entry indexEntry) (string, string) {
	if entry.URL == "" {
		return store.ImportRefused, "the index has no address for its file"
	}

	there, err := i.alreadyOnServer(ctx, entry.LevelID)
	if err != nil {
		return store.ImportFailed, err.Error()
	}

	if there {
		i.alreadyThere++
		return "", ""
	}

	course, err := entry.course()
	if err != nil {
		return store.ImportRefused, "the index entry can not be used: " + err.Error()
	}

	file, err := i.fromWaybackMachine(ctx, entry.URL)
	if err != nil {
		return store.ImportFailed, err.Error()
	}

	if file == nil {
		return store.ImportNotArchived, "the Wayback Machine has no copy of the file"
	}

	status, answer, err := i.send(ctx, course, file)
	if err != nil {
		return store.ImportFailed, err.Error()
	}

	switch status {
	case http.StatusCreated:
		i.imported++
		log.Printf("brought in %d %q by %s (%d)", entry.LevelID, entry.Name, entry.Creator, i.imported)

		return "", ""
	case http.StatusConflict:
		i.alreadyThere++
		return "", ""
	case http.StatusUnprocessableEntity:
		return store.ImportRefused, strings.TrimSpace(answer)
	default:
		return store.ImportFailed, fmt.Sprintf("the import service answered %d: %s", status, strings.TrimSpace(answer))
	}
}

func (e indexEntry) course() (importsvc.Course, error) {
	uploaded, err := time.Parse(time.RFC3339Nano, e.UploadTime)
	if err != nil {
		return importsvc.Course{}, fmt.Errorf("upload time %q: %w", e.UploadTime, err)
	}

	course := importsvc.Course{
		DataID:   e.LevelID,
		Name:     e.Name,
		Uploaded: uploaded,
		Maker:    importsvc.Player{PID: e.CreatorID, Name: e.Creator, Mii: decodeMii(e.CreatorMii)},
		Stars:    e.Stars,
		Clears:   e.Clears,
		Attempts: e.TotalAttempts,
	}

	if e.RecordMS > 0 && e.RecordHolder != "" {
		course.Record = &importsvc.Record{
			Milliseconds: e.RecordMS,
			Holder:       importsvc.Player{Name: e.RecordHolder, Mii: decodeMii(e.RecordHolderMii)},
		}
	}

	return course, nil
}

// * A Mii that can not be read is left out, and the service gives the
// * player a stand-in
func decodeMii(encoded string) []byte {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}

	return data
}

func (i *importer) alreadyOnServer(ctx context.Context, dataID uint64) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s%s/%d", i.service, importsvc.Path, dataID), nil)
	if err != nil {
		return false, err
	}

	request.Header.Set("Authorization", "Bearer "+i.token)

	response, err := i.client.Do(request)
	if err != nil {
		return false, fmt.Errorf("the import service could not be reached: %w", err)
	}

	response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("the import service answered %d", response.StatusCode)
	}
}

// * Returns nil when the Wayback Machine has no copy of the file
func (i *importer) fromWaybackMachine(ctx context.Context, original string) ([]byte, error) {
	file, err := i.fetch(ctx, i.wayback+original)
	if err != nil {
		return nil, err
	}

	if !bytes.HasPrefix(file, []byte("ASH0")) {
		return nil, nil
	}

	return file, nil
}

// * Every request to the archive goes through here: one at a time, with
// * a wait before each, and a longer wait each time it is told to slow
// * down or the connection fails. A request is given up after a few tries
func (i *importer) fetch(ctx context.Context, address string) ([]byte, error) {
	var lastErr error

	for try := 0; try < 6; try++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(i.wait + i.backoff):
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}

		request.Header.Set("User-Agent", agent)

		response, err := i.client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			lastErr = err
			i.slowDown(fmt.Sprintf("%s could not be reached", request.URL.Host))

			continue
		}

		body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		response.Body.Close()

		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable || response.StatusCode >= 520 {
			lastErr = fmt.Errorf("%s answered %d", request.URL.Host, response.StatusCode)
			i.slowDown(lastErr.Error())

			continue
		}

		if err != nil {
			lastErr = err
			i.slowDown(fmt.Sprintf("reading from %s failed", request.URL.Host))

			continue
		}

		// * Eased back once things go well again
		i.backoff /= 2

		if response.StatusCode == http.StatusNotFound {
			return nil, nil
		}

		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s answered %d", request.URL.Host, response.StatusCode)
		}

		return body, nil
	}

	return nil, fmt.Errorf("gave up after several tries: %w", lastErr)
}

func (i *importer) slowDown(why string) {
	i.backoff = min(max(2*i.backoff, 30*time.Second), 30*time.Minute)
	log.Printf("%s, waiting %s", why, i.backoff)
}

func (i *importer) send(ctx context.Context, course importsvc.Course, file []byte) (int, string, error) {
	var body bytes.Buffer

	form := multipart.NewWriter(&body)

	details, err := json.Marshal(course)
	if err != nil {
		return 0, "", err
	}

	if err := form.WriteField("course", string(details)); err != nil {
		return 0, "", err
	}

	part, err := form.CreateFormFile("file", "course.bin")
	if err != nil {
		return 0, "", err
	}

	if _, err := part.Write(file); err != nil {
		return 0, "", err
	}

	if err := form.Close(); err != nil {
		return 0, "", err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, i.service+importsvc.Path, &body)
	if err != nil {
		return 0, "", err
	}

	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+i.token)

	response, err := i.client.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("the import service could not be reached: %w", err)
	}

	defer response.Body.Close()

	answer, _ := io.ReadAll(io.LimitReader(response.Body, 4096))

	return response.StatusCode, string(answer), nil
}

func (i *importer) serviceRequest(ctx context.Context, method, path string, body any, into any) error {
	var reader io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}

		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, i.service+path, reader)
	if err != nil {
		return err
	}

	request.Header.Set("Authorization", "Bearer "+i.token)

	response, err := i.client.Do(request)
	if err != nil {
		return fmt.Errorf("the import service could not be reached: %w", err)
	}

	defer response.Body.Close()

	if response.StatusCode >= 300 {
		answer, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("the import service answered %d: %s", response.StatusCode, strings.TrimSpace(string(answer)))
	}

	if into != nil {
		return json.NewDecoder(response.Body).Decode(into)
	}

	return nil
}

func (i *importer) cursor(ctx context.Context, name string) (int64, error) {
	var cursor importsvc.Cursor
	err := i.serviceRequest(ctx, http.MethodGet, importsvc.CursorsPath+"/"+url.PathEscape(name), nil, &cursor)

	return cursor.Position, err
}

func (i *importer) setCursor(ctx context.Context, name string, position int64) error {
	return i.serviceRequest(ctx, http.MethodPut, importsvc.CursorsPath+"/"+url.PathEscape(name), importsvc.Cursor{Position: position}, nil)
}

func (i *importer) recordAttempt(ctx context.Context, entry indexEntry, outcome, detail string) error {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	return i.serviceRequest(ctx, http.MethodPost, importsvc.AttemptsPath, importsvc.Attempt{
		DataID:  entry.LevelID,
		Outcome: outcome,
		Detail:  detail,
		Entry:   encoded,
	}, nil)
}

func (i *importer) attemptsToRetry(ctx context.Context, maxTries, limit int) ([]importsvc.Attempt, error) {
	var attempts []importsvc.Attempt

	query := url.Values{
		"outcome":   {store.ImportFailed, store.ImportNotArchived},
		"max_tries": {strconv.Itoa(maxTries)},
		"limit":     {strconv.Itoa(limit)},
	}

	err := i.serviceRequest(ctx, http.MethodGet, importsvc.AttemptsPath+"?"+query.Encode(), nil, &attempts)

	return attempts, err
}
