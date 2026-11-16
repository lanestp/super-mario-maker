package datastore_db

import (
	"testing"

	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/super-mario-maker/database/dbtest"
)

func TestGetObjectRatingsWithSlotByDataIDs(t *testing.T) {
	dbtest.Open(t)

	dbtest.InsertCourse(t, dbtest.Course{DataID: 940000, RatingTotals: []int64{10, 20, 30}})
	dbtest.InsertCourse(t, dbtest.Course{DataID: 940001, RatingTotals: []int64{7}})
	dbtest.InsertCourse(t, dbtest.Course{DataID: 940002})
	dbtest.InsertCourse(t, dbtest.Course{DataID: 940003, RatingTotals: []int64{99}})

	ratings, nexError := GetObjectRatingsWithSlotByDataIDs([]types.UInt64{940000, 940001, 940002, 123})
	if nexError != nil {
		t.Fatal(nexError)
	}

	if len(ratings) != 2 {
		t.Fatalf("got ratings for %d objects, want 2 (objects without ratings and unrequested objects are left out)", len(ratings))
	}

	first := ratings[940000]
	if len(first) != 3 {
		t.Fatalf("object 940000 has %d ratings, want 3", len(first))
	}

	for slot, want := range []int64{10, 20, 30} {
		if int(first[slot].Slot) != slot || int64(first[slot].Rating.TotalValue) != want || first[slot].Rating.Count != 1 {
			t.Errorf("object 940000 slot %d = %+v, want slot %d with total %d and count 1", slot, first[slot], slot, want)
		}
	}

	if second := ratings[940001]; len(second) != 1 || second[0].Rating.TotalValue != 7 {
		t.Errorf("object 940001 ratings = %+v, want one rating with total 7", second)
	}
}

func TestGetObjectRatingsWithSlotByDataIDsEmpty(t *testing.T) {
	dbtest.Open(t)

	ratings, nexError := GetObjectRatingsWithSlotByDataIDs(nil)
	if nexError != nil {
		t.Fatal(nexError)
	}

	if len(ratings) != 0 {
		t.Fatalf("got %d ratings for no DataIDs, want 0", len(ratings))
	}
}
