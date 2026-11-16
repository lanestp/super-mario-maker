package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
	"github.com/PretendoNetwork/super-mario-maker/store/storetest"
)

func TestStore(t *testing.T) {
	var current *sqlite.Store

	storetest.Run(t, storetest.Backend{
		Open: func(t *testing.T) store.Store {
			opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
			if err != nil {
				t.Fatal(err)
			}

			current = opened

			return opened
		},
		SetUnderReview: func(t *testing.T, dataID uint64) {
			if _, err := current.DB().Exec(`UPDATE objects SET under_review=1 WHERE data_id=?`, int64(dataID)); err != nil {
				t.Fatal(err)
			}
		},
	})
}

func TestReopenKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smm.db")

	first, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := first.EnsureEventCourseMetadata(123); err != nil {
		t.Fatal(err)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	defer second.Close()

	size, nexError := second.GetObjectSizeByDataID(store.EventCourseMetadataDataID)
	if nexError != nil {
		t.Fatal(nexError)
	}

	if size != 123 {
		t.Errorf("got size %d after reopening, want 123", size)
	}
}
