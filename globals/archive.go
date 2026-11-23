package globals

import (
	"context"
	"time"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
)

// FetchFromArchive brings in a course a player asked for from the archive
// of Nintendo's servers, and says whether the server now has it. nil when
// fetching from the archive is turned off
var FetchFromArchive func(ctx context.Context, dataID uint64) bool

// ArchiveFill brings in courses from the whole archive in the background,
// until ctx ends. nil when it is turned off
var ArchiveFill func(ctx context.Context)

// * How long a game is kept waiting while a course it asked for is
// * fetched. Past that it is told the course is not there, and the fetch
// * carries on, so the course is there when it asks again
const archiveWait = 8 * time.Second

// FromArchive fetches a course the server does not have from the archive,
// when that is turned on, and says whether the server now has it
func FromArchive(dataID types.UInt64) bool {
	if FetchFromArchive == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), archiveWait)
	defer cancel()

	return FetchFromArchive(ctx, uint64(dataID))
}

// ObjectInfoOrFromArchive looks an object up, and when it is not on the
// server, fetches it from the archive and looks again
func ObjectInfoOrFromArchive(dataID types.UInt64, lookUp func(types.UInt64) (datastore_types.DataStoreMetaInfo, *nex.Error)) (datastore_types.DataStoreMetaInfo, *nex.Error) {
	info, nexError := lookUp(dataID)

	if nexError != nil && isNotFound(nexError) && FromArchive(dataID) {
		return lookUp(dataID)
	}

	return info, nexError
}

// * nex.NewError sets the top bit of a result code, to mark it as an
// * error, so codes are compared with it set on both sides
func isNotFound(nexError *nex.Error) bool {
	const errorBit = 0x80000000

	return nexError.ResultCode|errorBit == nex.ResultCodes.DataStore.NotFound|errorBit
}
