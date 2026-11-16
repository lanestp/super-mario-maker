package datastore_db

import (
	"database/sql"

	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/lib/pq"
)

type pidArray struct {
	pids *types.List[types.PID]
}

func (a pidArray) Scan(src any) error {
	var values pq.Int64Array

	if err := values.Scan(src); err != nil {
		return err
	}

	*a.pids = make(types.List[types.PID], 0, len(values))
	for i := range values {
		*a.pids = append(*a.pids, types.PID(values[i]))
	}

	return nil
}

// PIDArray scans a Postgres integer array into a list of PIDs.
// pq.Array can not do this itself, as it does not know how to scan a PID
func PIDArray(pids *types.List[types.PID]) sql.Scanner {
	return pidArray{pids: pids}
}
