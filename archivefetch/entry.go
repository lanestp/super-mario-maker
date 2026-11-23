package archivefetch

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/PretendoNetwork/super-mario-maker/seed"
)

// Entry is what the archive's index says about a course. The index
// answers a lookup by ID with one, and a lookup by position with a list
type Entry struct {
	URL             string `json:"url"`
	Name            string `json:"name"`
	Creator         string `json:"creator"`
	LevelID         uint64 `json:"levelid"`
	CreatorID       uint32 `json:"creatorid"`
	Clears          int    `json:"clears"`
	TotalAttempts   int    `json:"total_attempts"`
	UserPlays       int    `json:"user_plays"`
	UploadTime      string `json:"uploadTime"`
	RecordMS        int    `json:"world_record_ms"`
	RecordHolder    string `json:"world_record_holder_nnid"`
	RecordHolderPID uint32 `json:"world_record_best_time_player_pid"`
	RecordHolderMii string `json:"world_record_best_time_player_mii_data"`
	CreatorMii      string `json:"creator_mii_data"`
	Stars           int    `json:"stars"`
	// Captured is when the Wayback Machine captured the file at URL, as
	// its index gives it. Empty when not known, and the capture nearest
	// to the archive's crawl is asked for
	Captured string `json:"captured,omitempty"`
}

// Archived is the course as the seeder takes it, with its file
func (e Entry) Archived(file []byte) (seed.ArchivedCourse, error) {
	uploaded, err := time.Parse(time.RFC3339Nano, e.UploadTime)
	if err != nil {
		return seed.ArchivedCourse{}, fmt.Errorf("upload time %q: %w", e.UploadTime, err)
	}

	course := seed.ArchivedCourse{
		DataID:   e.LevelID,
		File:     file,
		Name:     e.Name,
		Uploaded: uploaded,
		Maker:    seed.ArchivedPlayer{PID: e.CreatorID, Name: e.Creator, MakerMetaBinary: decodeMii(e.CreatorMii)},
		Stars:    e.Stars,
		Clears:   e.Clears,
		Attempts: e.TotalAttempts,
		Plays:    e.UserPlays,
	}

	if e.RecordMS > 0 && e.RecordHolder != "" {
		course.Record = &seed.ArchivedRecord{
			Milliseconds: e.RecordMS,
			Holder:       seed.ArchivedPlayer{PID: e.RecordHolderPID, Name: e.RecordHolder, MakerMetaBinary: decodeMii(e.RecordHolderMii)},
		}
	}

	return course, nil
}

// * A Mii that can not be read is left out, and the seeder gives the
// * player a stand-in
func decodeMii(encoded string) []byte {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}

	return data
}

func jsonOf(entry Entry) ([]byte, error) {
	return json.Marshal(entry)
}
