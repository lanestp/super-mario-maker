// Package importsvc serves the import service: a course file and what an
// archive of Nintendo's servers says about it go in, are checked, and
// are written to storage and the database. It is only for the server's
// own tools, so it is only served on the machine's loopback address, and
// needs a token
package importsvc

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/super-mario-maker/seed"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

// Path is where courses are sent
const Path = "/v1/import/courses"

// CursorsPath is where how far a run through an archive has got is kept
const CursorsPath = "/v1/import/cursors"

// Cursor is how far a run has got
type Cursor struct {
	Position int64 `json:"position"`
}

// AttemptsPath is where courses that were not brought in are kept track of
const AttemptsPath = "/v1/import/attempts"

// Attempt is a course that was not brought in, as sent to and from the
// service. Outcome is one of store.ImportFailed, store.ImportNotArchived
// and store.ImportRefused
type Attempt struct {
	DataID    uint64          `json:"data_id"`
	Outcome   string          `json:"outcome"`
	Detail    string          `json:"detail"`
	Entry     json.RawMessage `json:"entry"`
	Tries     int             `json:"tries,omitempty"`
	LastTried time.Time       `json:"last_tried,omitempty"`
}

// * The largest course file taken. Course files are tens of kilobytes
const maxFileBytes = 1 << 20

// Player is a player as sent to the service
type Player struct {
	PID  uint32 `json:"pid,omitempty"`
	Name string `json:"name"`
	// Mii is the meta binary of their maker object, which holds their Mii
	Mii []byte `json:"mii,omitempty"`
}

// Record is a course's fastest clear as sent to the service
type Record struct {
	Milliseconds int    `json:"milliseconds"`
	Holder       Player `json:"holder"`
}

// Course is what is sent about a course, along with its file
type Course struct {
	DataID   uint64    `json:"data_id"`
	Name     string    `json:"name"`
	Uploaded time.Time `json:"uploaded"`
	Maker    Player    `json:"maker"`
	Stars    int       `json:"stars"`
	Clears   int       `json:"clears"`
	Attempts int       `json:"attempts"`
	Plays    int       `json:"plays,omitempty"`
	Record   *Record   `json:"record,omitempty"`
}

// Config is what the service needs
type Config struct {
	Seeder *seed.Seeder
	// Token is what a tool has to send, as a bearer token
	Token string
	// LogError is told about errors that are not the sender's
	LogError func(error)
}

type service struct {
	config Config
}

// Handler serves the service at Path
func Handler(config Config) (http.Handler, error) {
	if config.Seeder == nil || config.Token == "" {
		return nil, errors.New("importsvc: a seeder and a token are needed")
	}

	if config.LogError == nil {
		config.LogError = func(error) {}
	}

	s := &service{config: config}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+Path+"/{id}", s.exists)
	mux.HandleFunc("POST "+Path, s.importCourse)
	mux.HandleFunc("POST "+AttemptsPath, s.recordAttempt)
	mux.HandleFunc("GET "+AttemptsPath, s.attemptsToRetry)
	mux.HandleFunc("GET "+CursorsPath+"/{name}", s.cursor)
	mux.HandleFunc("PUT "+CursorsPath+"/{name}", s.setCursor)

	return s.authorized(mux), nil
}

func (s *service) authorized(next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(s.config.Token))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		got := sha256.Sum256([]byte(token))

		if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
			http.Error(w, "a token is needed", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// * Answers 200 when a course is on the server, so that a tool does not
// * fetch one it would not be able to add
func (s *service) exists(w http.ResponseWriter, r *http.Request) {
	dataID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "not a DataID", http.StatusBadRequest)
		return
	}

	if _, nexError := s.config.Seeder.Store.GetObjectInfoByDataID(types.UInt64(dataID)); nexError != nil {
		http.Error(w, "not on the server", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// * Takes a multipart form with a "course" part, the details as JSON, and
// * a "file" part, the course file as a game uploads it
func (s *service) importCourse(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFileBytes+64<<10)

	if err := r.ParseMultipartForm(maxFileBytes + 64<<10); err != nil {
		http.Error(w, "the form could not be read, or is too large", http.StatusBadRequest)
		return
	}

	var course Course
	if err := json.Unmarshal([]byte(r.FormValue("course")), &course); err != nil {
		http.Error(w, "the course details are not valid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "the form has no course file", http.StatusBadRequest)
		return
	}

	data, err := io.ReadAll(file)
	file.Close()

	if err != nil {
		http.Error(w, "the course file could not be read", http.StatusBadRequest)
		return
	}

	archived := seed.ArchivedCourse{
		DataID:   course.DataID,
		File:     data,
		Name:     course.Name,
		Uploaded: course.Uploaded,
		Maker:    seed.ArchivedPlayer{PID: course.Maker.PID, Name: course.Maker.Name, MakerMetaBinary: course.Maker.Mii},
		Stars:    course.Stars,
		Clears:   course.Clears,
		Attempts: course.Attempts,
		Plays:    course.Plays,
	}

	if course.Record != nil {
		archived.Record = &seed.ArchivedRecord{
			Milliseconds: course.Record.Milliseconds,
			Holder:       seed.ArchivedPlayer{PID: course.Record.Holder.PID, Name: course.Record.Holder.Name, MakerMetaBinary: course.Record.Holder.Mii},
		}
	}

	err = s.config.Seeder.ImportCourse(archived)

	// * A course that is here needs no more tries
	if err == nil || errors.Is(err, seed.ErrAlreadyImported) {
		if forgetErr := s.config.Seeder.Store.ForgetImportAttempt(course.DataID); forgetErr != nil {
			s.config.LogError(fmt.Errorf("forgetting the attempts at course %d: %w", course.DataID, forgetErr))
		}
	}

	switch {
	case err == nil:
		w.WriteHeader(http.StatusCreated)
	case errors.Is(err, seed.ErrAlreadyImported):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, seed.ErrInvalidCourse):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	default:
		s.config.LogError(fmt.Errorf("importing course %d: %w", course.DataID, err))
		http.Error(w, "the course could not be stored", http.StatusInternalServerError)
	}
}

func (s *service) recordAttempt(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)

	var attempt Attempt
	if err := json.NewDecoder(r.Body).Decode(&attempt); err != nil {
		http.Error(w, "the attempt is not valid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	switch attempt.Outcome {
	case store.ImportFailed, store.ImportNotArchived, store.ImportRefused:
	default:
		http.Error(w, "the outcome must be failed, not_archived or refused", http.StatusBadRequest)
		return
	}

	if attempt.DataID == 0 {
		http.Error(w, "the attempt has no DataID", http.StatusBadRequest)
		return
	}

	err := s.config.Seeder.Store.RecordImportAttempt(store.ImportAttempt{
		DataID:  attempt.DataID,
		Outcome: attempt.Outcome,
		Detail:  attempt.Detail,
		Entry:   attempt.Entry,
	})
	if err != nil {
		s.config.LogError(fmt.Errorf("recording an attempt at course %d: %w", attempt.DataID, err))
		http.Error(w, "the attempt could not be recorded", http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// * Lists courses to try again: those with any of the outcome
// * parameters, tried fewer than max_tries times, at most limit of them
func (s *service) attemptsToRetry(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	maxTries, err := strconv.Atoi(query.Get("max_tries"))
	if err != nil || maxTries < 1 {
		maxTries = 3
	}

	limit, err := strconv.Atoi(query.Get("limit"))
	if err != nil || limit < 1 || limit > 10000 {
		limit = 100
	}

	found, err := s.config.Seeder.Store.ImportAttemptsToRetry(query["outcome"], maxTries, limit)
	if err != nil {
		s.config.LogError(fmt.Errorf("listing attempts: %w", err))
		http.Error(w, "the attempts could not be listed", http.StatusInternalServerError)

		return
	}

	attempts := make([]Attempt, 0, len(found))
	for _, attempt := range found {
		attempts = append(attempts, Attempt{
			DataID:    attempt.DataID,
			Outcome:   attempt.Outcome,
			Detail:    attempt.Detail,
			Entry:     json.RawMessage(attempt.Entry),
			Tries:     attempt.Tries,
			LastTried: attempt.LastTried,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(attempts)
}

func (s *service) cursor(w http.ResponseWriter, r *http.Request) {
	position, err := s.config.Seeder.Store.ImportCursor(r.PathValue("name"))
	if err != nil {
		s.config.LogError(fmt.Errorf("reading cursor %q: %w", r.PathValue("name"), err))
		http.Error(w, "the cursor could not be read", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Cursor{Position: position})
}

func (s *service) setCursor(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	var cursor Cursor
	if err := json.NewDecoder(r.Body).Decode(&cursor); err != nil || cursor.Position < 0 {
		http.Error(w, "the cursor must be a position of 0 or more", http.StatusBadRequest)
		return
	}

	if err := s.config.Seeder.Store.SetImportCursor(r.PathValue("name"), cursor.Position); err != nil {
		s.config.LogError(fmt.Errorf("saving cursor %q: %w", r.PathValue("name"), err))
		http.Error(w, "the cursor could not be saved", http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
