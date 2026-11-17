package nex_datastore_super_mario_maker

import (
	"testing"

	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

func extra(values ...string) types.List[types.String] {
	list := types.NewList[types.String]()
	for _, value := range values {
		list = append(list, types.String(value))
	}

	return list
}

func TestDifficultyFilterReadsWhatARealGameSends(t *testing.T) {
	for name, try := range map[string]struct {
		extraData types.List[types.String]
		want      store.FailureRateFilter
	}{
		"100 Mario easy":            {extra("1", "0", "34", "", "0"), store.FailureRateFilter{MinPercent: 0, MaxPercent: 34, MinAttempts: MinAttemptsForDifficulty}},
		"100 Mario normal":          {extra("1", "0", "74", "", "0"), store.FailureRateFilter{MinPercent: 0, MaxPercent: 74, MinAttempts: MinAttemptsForDifficulty}},
		"Course World normal":       {extra("1", "35", "74", "0", "0"), store.FailureRateFilter{MinPercent: 35, MaxPercent: 74, MinAttempts: MinAttemptsForDifficulty}},
		"Course World super expert": {extra("1", "96", "100", "0", "0"), store.FailureRateFilter{MinPercent: 96, MaxPercent: 100, MinAttempts: MinAttemptsForDifficulty}},
	} {
		got, byDifficulty := DifficultyFilter(try.extraData)
		if !byDifficulty || got != try.want {
			t.Errorf("%s: got %+v (%v), want %+v", name, got, byDifficulty, try.want)
		}
	}
}

func TestDifficultyFilterTakesAnyCourseWhenNoDifficultyIsAskedFor(t *testing.T) {
	for name, extraData := range map[string]types.List[types.String]{
		"Course World all": extra("", "", "", "0", "0"),
		"nothing":          extra(),
		"too short":        extra("1", "0"),
		"not numbers":      extra("1", "easy", "hard", "", "0"),
		"upside down":      extra("1", "74", "35", "", "0"),
		"over a hundred":   extra("1", "0", "150", "", "0"),
		"a negative":       extra("1", "-5", "34", "", "0"),
	} {
		if got, byDifficulty := DifficultyFilter(extraData); byDifficulty {
			t.Errorf("%s: filtered by %+v, want any course", name, got)
		}
	}
}
