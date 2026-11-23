package globals

import (
	"context"
	"testing"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
)

func TestALookupThatMissesFetchesFromTheArchiveAndLooksAgain(t *testing.T) {
	t.Cleanup(func() { FetchFromArchive = nil })

	here := map[types.UInt64]bool{}
	lookUp := func(dataID types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error) {
		if !here[dataID] {
			return datastore_types.NewDataStoreMetaInfo(), nex.NewError(nex.ResultCodes.DataStore.NotFound, "Object not found")
		}

		info := datastore_types.NewDataStoreMetaInfo()
		info.DataID = dataID

		return info, nil
	}

	// * Turned off, a miss is a miss
	if _, nexError := ObjectInfoOrFromArchive(5, lookUp); nexError == nil {
		t.Fatal("found a course with fetching turned off")
	}

	var asked []uint64
	FetchFromArchive = func(_ context.Context, dataID uint64) bool {
		asked = append(asked, dataID)
		here[types.UInt64(dataID)] = dataID == 5

		return dataID == 5
	}

	if info, nexError := ObjectInfoOrFromArchive(5, lookUp); nexError != nil || info.DataID != 5 {
		t.Errorf("got %d (%v), want course 5 once fetched", info.DataID, nexError)
	}

	if _, nexError := ObjectInfoOrFromArchive(6, lookUp); nexError == nil || !isNotFound(nexError) {
		t.Errorf("got %v for a course the archive does not have, want not found", nexError)
	}

	// * Another error is passed on, with nothing fetched
	failing := func(types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error) {
		return datastore_types.NewDataStoreMetaInfo(), nex.NewError(nex.ResultCodes.DataStore.PermissionDenied, "Permission denied")
	}

	if _, nexError := ObjectInfoOrFromArchive(7, failing); nexError == nil || nexError.ResultCode|0x80000000 != nex.ResultCodes.DataStore.PermissionDenied|0x80000000 {
		t.Errorf("got %v, want the permission error passed on", nexError)
	}

	if len(asked) != 2 {
		t.Errorf("fetched %v, want 5 and 6 only", asked)
	}
}
