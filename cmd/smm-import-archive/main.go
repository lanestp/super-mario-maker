// Command smm-import-archive brings courses from the archive of Nintendo's
// Super Mario Maker servers into a server, one at a time.
//
// It picks courses at random from a public index of the archive, fetches
// each course file from the Wayback Machine, and sends it with what the
// index says about it to the server's import service, which checks it
// and writes it. Nothing is kept on disk.
//
//	smm-import-archive -count 10000
//
// PN_SMM_IMPORT_TOKEN is the import service's token, and
// PN_SMM_IMPORT_ADDRESS where it is (127.0.0.1:8384 when not set). It
// waits between every request to the archive, and longer when told to
// slow down, so it takes hours for thousands of courses
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/PretendoNetwork/super-mario-maker/importsvc"
)

const (
	indexURL = "https://api.bobac-analytics.com/smm1/searchRandomLevels/"
	// * The index picks a course at or after a position below this
	indexRange = 18118278

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
	client  *http.Client
	service string
	token   string
	wait    time.Duration
	backoff time.Duration

	imported, alreadyThere, notArchived, refused, failed int
}

func main() {
	count := flag.Int("count", 100, "how many courses to bring in")
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	i := &importer{
		client:  &http.Client{Timeout: time.Minute},
		service: "http://" + address + importsvc.Path,
		token:   token,
		wait:    *wait,
	}

	i.run(ctx, *count)
}

func (i *importer) run(ctx context.Context, count int) {
	started := time.Now()
	consecutiveFailures := 0

	for tries := 0; i.imported < count && tries < count*30 && ctx.Err() == nil; tries++ {
		entries, err := i.pickFromIndex(ctx)
		if err != nil {
			consecutiveFailures++
			log.Printf("the index could not be read: %v", err)

			if consecutiveFailures >= 50 {
				log.Print("stopping after 50 failures in a row")
				break
			}

			continue
		}

		for _, entry := range entries {
			if i.imported >= count || ctx.Err() != nil {
				break
			}

			if err := i.bringIn(ctx, entry); err != nil {
				consecutiveFailures++
				i.failed++
				log.Printf("course %d: %v", entry.LevelID, err)
			} else {
				consecutiveFailures = 0
			}
		}

		if consecutiveFailures >= 50 {
			log.Print("stopping after 50 failures in a row")
			break
		}

		if i.imported > 0 && i.imported%25 == 0 {
			i.report(started)
		}
	}

	i.report(started)
}

func (i *importer) report(started time.Time) {
	log.Printf("%d brought in, %d already there, %d not archived, %d refused, %d failed, after %s",
		i.imported, i.alreadyThere, i.notArchived, i.refused, i.failed, time.Since(started).Round(time.Minute))
}

func (i *importer) pickFromIndex(ctx context.Context) ([]indexEntry, error) {
	body, err := i.fetch(ctx, indexURL+fmt.Sprint(rand.IntN(indexRange)))
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

func (i *importer) bringIn(ctx context.Context, entry indexEntry) error {
	if entry.LevelID == 0 || entry.URL == "" {
		return nil
	}

	there, err := i.alreadyOnServer(ctx, entry.LevelID)
	if err != nil {
		return err
	}

	if there {
		i.alreadyThere++
		return nil
	}

	file, err := i.fromWaybackMachine(ctx, entry.URL)
	if err != nil {
		return err
	}

	if file == nil {
		i.notArchived++
		return nil
	}

	course, err := entry.course()
	if err != nil {
		i.refused++
		log.Printf("course %d: the index entry can not be used: %v", entry.LevelID, err)

		return nil
	}

	status, answer, err := i.send(ctx, course, file)
	if err != nil {
		return err
	}

	switch status {
	case http.StatusCreated:
		i.imported++
		log.Printf("brought in %d %q by %s (%d)", entry.LevelID, entry.Name, entry.Creator, i.imported)
	case http.StatusConflict:
		i.alreadyThere++
	case http.StatusUnprocessableEntity:
		i.refused++
		log.Printf("course %d was refused: %s", entry.LevelID, strings.TrimSpace(answer))
	default:
		return fmt.Errorf("the import service answered %d: %s", status, strings.TrimSpace(answer))
	}

	return nil
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/%d", i.service, dataID), nil)
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
	body, err := i.fetch(ctx, "https://archive.org/wayback/available?url="+url.QueryEscape(original))
	if err != nil {
		return nil, err
	}

	var available struct {
		Snapshots struct {
			Closest struct {
				Available bool   `json:"available"`
				Timestamp string `json:"timestamp"`
				Status    string `json:"status"`
			} `json:"closest"`
		} `json:"archived_snapshots"`
	}

	if err := json.Unmarshal(body, &available); err != nil {
		return nil, fmt.Errorf("the Wayback Machine's answer could not be read: %w", err)
	}

	closest := available.Snapshots.Closest
	if !closest.Available || closest.Status != "200" {
		return nil, nil
	}

	// * "if_" asks for the file as it was, and not wrapped in a page
	file, err := i.fetch(ctx, "https://web.archive.org/web/"+closest.Timestamp+"if_/"+original)
	if err != nil {
		return nil, err
	}

	if !bytes.HasPrefix(file, []byte("ASH0")) {
		return nil, nil
	}

	return file, nil
}

var errSlowDown = errors.New("told to slow down")

// * Every request to the archive goes through here: one at a time, with
// * a wait before each, and a longer wait each time it is told to slow down
func (i *importer) fetch(ctx context.Context, address string) ([]byte, error) {
	for {
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
			return nil, err
		}

		body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		response.Body.Close()

		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable {
			i.backoff = min(max(2*i.backoff, time.Minute), 30*time.Minute)
			log.Printf("%v, waiting %s", errSlowDown, i.backoff)

			continue
		}

		// * Eased back once things go well again
		i.backoff /= 2

		if err != nil {
			return nil, err
		}

		if response.StatusCode == http.StatusNotFound {
			return nil, nil
		}

		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s answered %d", request.URL.Host, response.StatusCode)
		}

		return body, nil
	}
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

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, i.service, &body)
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
