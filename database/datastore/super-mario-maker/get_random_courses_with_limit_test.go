package datastore_smm_db

import (
	"fmt"
	"testing"

	"github.com/PretendoNetwork/super-mario-maker/database/dbtest"
)

// * Inserts count available courses starting at DataID 940000,
// * each with 2 rating slots
func insertCourses(t *testing.T, count int) {
	t.Helper()

	for i := range count {
		dbtest.InsertCourse(t, dbtest.Course{
			DataID:       uint64(940000 + i),
			Owner:        uint32(1000 + i),
			Name:         fmt.Sprintf("course %d", i),
			Score:        uint32(i),
			RatingTotals: []int64{int64(i), int64(i * 2)},
		})
	}
}

func TestGetRandomCoursesWithLimitNoCourses(t *testing.T) {
	dbtest.Open(t)

	courses, nexError := GetRandomCoursesWithLimit(50)
	if nexError != nil {
		t.Fatal(nexError)
	}

	if len(courses) != 0 {
		t.Fatalf("got %d courses from an empty database, want 0", len(courses))
	}
}

func TestGetRandomCoursesWithLimitReturnsRequestedAmount(t *testing.T) {
	dbtest.Open(t)
	insertCourses(t, 300)

	for _, limit := range []int{1, 25, 50, 100} {
		for range 20 {
			courses, nexError := GetRandomCoursesWithLimit(limit)
			if nexError != nil {
				t.Fatal(nexError)
			}

			if len(courses) != limit {
				t.Fatalf("limit %d: got %d courses", limit, len(courses))
			}

			seen := make(map[uint64]bool, len(courses))
			for _, course := range courses {
				dataID := uint64(course.MetaInfo.DataID)
				if seen[dataID] {
					t.Fatalf("limit %d: course %d returned twice", limit, dataID)
				}

				seen[dataID] = true
			}
		}
	}
}

func TestGetRandomCoursesWithLimitFewerCoursesThanRequested(t *testing.T) {
	dbtest.Open(t)
	insertCourses(t, 40)

	for range 20 {
		courses, nexError := GetRandomCoursesWithLimit(50)
		if nexError != nil {
			t.Fatal(nexError)
		}

		if len(courses) != 40 {
			t.Fatalf("got %d courses, want all 40", len(courses))
		}
	}
}

func TestGetRandomCoursesWithLimitFillsCourseFields(t *testing.T) {
	dbtest.Open(t)
	insertCourses(t, 10)

	courses, nexError := GetRandomCoursesWithLimit(10)
	if nexError != nil {
		t.Fatal(nexError)
	}

	if len(courses) != 10 {
		t.Fatalf("got %d courses, want 10", len(courses))
	}

	for _, course := range courses {
		i := int(course.MetaInfo.DataID) - 940000

		if string(course.MetaInfo.Name) != fmt.Sprintf("course %d", i) {
			t.Errorf("course %d has name %q", course.MetaInfo.DataID, course.MetaInfo.Name)
		}

		if int(course.MetaInfo.OwnerID) != 1000+i {
			t.Errorf("course %d has owner %d, want %d", course.MetaInfo.DataID, course.MetaInfo.OwnerID, 1000+i)
		}

		if int(course.Score) != i {
			t.Errorf("course %d has score %d, want %d", course.MetaInfo.DataID, course.Score, i)
		}

		if course.Order != 0 {
			t.Errorf("course %d has order %d, want 0", course.MetaInfo.DataID, course.Order)
		}

		if len(course.MetaInfo.MetaBinary) != 2 {
			t.Errorf("course %d has %d bytes of meta binary, want 2", course.MetaInfo.DataID, len(course.MetaInfo.MetaBinary))
		}

		if len(course.MetaInfo.Tags) != 1 || string(course.MetaInfo.Tags[0]) != "tag" {
			t.Errorf("course %d has tags %v, want [tag]", course.MetaInfo.DataID, course.MetaInfo.Tags)
		}

		if len(course.MetaInfo.Ratings) != 2 {
			t.Errorf("course %d has %d ratings, want 2", course.MetaInfo.DataID, len(course.MetaInfo.Ratings))
			continue
		}

		if int(course.MetaInfo.Ratings[0].Rating.TotalValue) != i || int(course.MetaInfo.Ratings[1].Rating.TotalValue) != i*2 {
			t.Errorf("course %d has rating totals %d and %d, want %d and %d", course.MetaInfo.DataID, course.MetaInfo.Ratings[0].Rating.TotalValue, course.MetaInfo.Ratings[1].Rating.TotalValue, i, i*2)
		}
	}
}

func TestGetRandomCoursesWithLimitSkipsUnavailableObjects(t *testing.T) {
	dbtest.Open(t)

	available := map[uint64]bool{}

	// * Unavailable objects are placed all around the available
	// * ones, including at both ends of the DataID range
	for i := range 200 {
		course := dbtest.Course{DataID: uint64(940000 + i)}

		switch i % 5 {
		case 0:
			course.Deleted = true
		case 1:
			course.UnderReview = true
		case 2:
			course.UploadIncomplete = true
		case 3:
			course.NoRanking = true
		default:
			available[course.DataID] = true
		}

		dbtest.InsertCourse(t, course)
	}

	for _, limit := range []int{10, 40, 100} {
		for range 20 {
			courses, nexError := GetRandomCoursesWithLimit(limit)
			if nexError != nil {
				t.Fatal(nexError)
			}

			want := min(limit, len(available))
			if len(courses) != want {
				t.Fatalf("limit %d: got %d courses, want %d", limit, len(courses), want)
			}

			for _, course := range courses {
				if !available[uint64(course.MetaInfo.DataID)] {
					t.Fatalf("limit %d: unavailable object %d returned", limit, course.MetaInfo.DataID)
				}
			}
		}
	}
}

func TestGetRandomCoursesWithLimitBounds(t *testing.T) {
	dbtest.Open(t)
	insertCourses(t, 150)

	for _, limit := range []int{0, -1} {
		courses, nexError := GetRandomCoursesWithLimit(limit)
		if nexError != nil {
			t.Fatal(nexError)
		}

		if len(courses) != 0 {
			t.Errorf("limit %d: got %d courses, want 0", limit, len(courses))
		}
	}

	courses, nexError := GetRandomCoursesWithLimit(MaxRandomCourses + 1000)
	if nexError != nil {
		t.Fatal(nexError)
	}

	if len(courses) != MaxRandomCourses {
		t.Errorf("got %d courses, want the maximum of %d", len(courses), MaxRandomCourses)
	}
}

func TestGetRandomCoursesWithLimitVaries(t *testing.T) {
	dbtest.Open(t)
	insertCourses(t, 300)

	seen := map[uint64]bool{}

	for range 30 {
		courses, nexError := GetRandomCoursesWithLimit(25)
		if nexError != nil {
			t.Fatal(nexError)
		}

		for _, course := range courses {
			seen[uint64(course.MetaInfo.DataID)] = true
		}
	}

	// * 750 picks from 300 courses. Seeing under half
	// * of them would mean the picks are not random
	if len(seen) < 150 {
		t.Errorf("only %d of 300 courses were ever returned", len(seen))
	}
}
