package globals_test

import (
	"path/filepath"
	"testing"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/accounts"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

func TestPasswordFromPIDWithLocalAccounts(t *testing.T) {
	globals.Logger = plogger.NewLogger()

	opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
	if err != nil {
		t.Fatal(err)
	}

	previousStore, previousLocal := globals.Store, globals.LocalAccounts
	globals.Store, globals.LocalAccounts = opened, true

	t.Cleanup(func() {
		globals.Store, globals.LocalAccounts = previousStore, previousLocal
		opened.Close()
	})

	account, err := accounts.Create(opened, "PlayerOne", "secret123", "")
	if err != nil {
		t.Fatal(err)
	}

	password, resultCode := globals.PasswordFromPID(types.PID(account.PID))
	if resultCode != 0 || password != account.NEXPassword {
		t.Errorf("got password %q and result code %#x, want the account's NEX password and 0", password, resultCode)
	}

	password, resultCode = globals.PasswordFromPID(types.PID(account.PID + 1))
	if resultCode != nex.ResultCodes.RendezVous.InvalidUsername || password != "" {
		t.Errorf("an unknown PID gave password %q and result code %#x, want none and InvalidUsername", password, resultCode)
	}
}
