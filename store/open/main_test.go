package open_test

import (
	"testing"

	"github.com/PretendoNetwork/super-mario-maker/database/dbtest"
)

// * Stops the Postgres the tests start, if they start one
func TestMain(m *testing.M) { dbtest.Main(m) }
