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
)

// Path is where the service is served
const Path = "/v1/import/courses"

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
	}

	if course.Record != nil {
		archived.Record = &seed.ArchivedRecord{
			Milliseconds: course.Record.Milliseconds,
			Holder:       seed.ArchivedPlayer{PID: course.Record.Holder.PID, Name: course.Record.Holder.Name, MakerMetaBinary: course.Record.Holder.Mii},
		}
	}

	err = s.config.Seeder.ImportCourse(archived)

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
