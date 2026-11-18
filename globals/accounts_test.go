package globals_test

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	pb "github.com/PretendoNetwork/grpc/go/account"
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/accounts"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// * Points the globals at a new SQLite store holding one account, and
// * puts them back after the test
func localAccounts(t *testing.T) store.Account {
	t.Helper()

	globals.Logger = plogger.NewLogger()

	opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
	if err != nil {
		t.Fatal(err)
	}

	previousStore, previousLocal := globals.Store, globals.LocalAccounts
	previousAuthentication, previousSecure := globals.AuthenticationServerAccount, globals.SecureServerAccount

	globals.Store, globals.LocalAccounts = opened, true
	globals.AuthenticationServerAccount = nex.NewAccount(1, "Quazal Authentication", "kerberos")
	globals.SecureServerAccount = nex.NewAccount(2, "Quazal Rendez-Vous", "kerberos")

	t.Cleanup(func() {
		globals.Store, globals.LocalAccounts = previousStore, previousLocal
		globals.AuthenticationServerAccount, globals.SecureServerAccount = previousAuthentication, previousSecure
		opened.Close()
	})

	account, err := accounts.Create(opened, "PlayerOne", "secret123", "")
	if err != nil {
		t.Fatal(err)
	}

	return account
}

func TestTheServersOwnAccountsAreFoundByPIDAndByName(t *testing.T) {
	localAccounts(t)

	for _, special := range []*nex.Account{globals.AuthenticationServerAccount, globals.SecureServerAccount} {
		byPID, nexError := globals.AccountDetailsByPID(special.PID)
		if nexError != nil || byPID != special {
			t.Errorf("PID %d gave %v (%v), want the server's own account", special.PID, byPID, nexError)
		}

		byName, nexError := globals.AccountDetailsByUsername(special.Username)
		if nexError != nil || byName != special {
			t.Errorf("username %q gave %v (%v), want the server's own account", special.Username, byName, nexError)
		}
	}
}

func TestAPlayersAccountIsFoundByPIDAndByThePIDAsAName(t *testing.T) {
	account := localAccounts(t)

	byPID, nexError := globals.AccountDetailsByPID(types.PID(account.PID))
	if nexError != nil {
		t.Fatal(nexError)
	}

	// * A player's name on the game server is their PID
	if byPID.PID != types.PID(account.PID) || byPID.Username != strconv.Itoa(int(account.PID)) || byPID.Password != account.NEXPassword {
		t.Errorf("got %+v, want PID %d named by its PID with the NEX password", byPID, account.PID)
	}

	byName, nexError := globals.AccountDetailsByUsername(strconv.Itoa(int(account.PID)))
	if nexError != nil {
		t.Fatal(nexError)
	}

	if byName.PID != types.PID(account.PID) || byName.Password != account.NEXPassword {
		t.Errorf("got %+v, want the account with its NEX password", byName)
	}
}

// * The result code an error made from InvalidUsername has, which has
// * the bit set that marks it as an error
var invalidUsername = nex.NewError(nex.ResultCodes.RendezVous.InvalidUsername, "").ResultCode

func TestAnUnknownPlayerIsRefused(t *testing.T) {
	account := localAccounts(t)

	if _, nexError := globals.AccountDetailsByPID(types.PID(account.PID + 1)); nexError == nil || nexError.ResultCode != invalidUsername {
		t.Errorf("an unknown PID gave %v, want InvalidUsername", nexError)
	}

	if _, nexError := globals.AccountDetailsByUsername(strconv.Itoa(int(account.PID + 1))); nexError == nil || nexError.ResultCode != invalidUsername {
		t.Errorf("an unknown PID as a name gave %v, want InvalidUsername", nexError)
	}

	// * A name that is not a PID is not looked up at all
	if _, nexError := globals.AccountDetailsByUsername("PlayerOne"); nexError == nil || nexError.ResultCode != invalidUsername || nexError.Message != "Invalid username" {
		t.Errorf("the name PlayerOne gave %v, want InvalidUsername", nexError)
	}
}

// * An account server that knows one PID, and remembers the key it was sent
type fakeAccountServer struct {
	pb.AccountClient

	pid      uint32
	password string
	keys     []string
}

func (f *fakeAccountServer) GetNEXPassword(ctx context.Context, request *pb.GetNEXPasswordRequest, _ ...grpc.CallOption) (*pb.GetNEXPasswordResponse, error) {
	sent, _ := metadata.FromOutgoingContext(ctx)
	f.keys = append(f.keys, sent.Get("X-API-Key")...)

	if request.Pid != f.pid {
		return nil, errors.New("no such account")
	}

	return &pb.GetNEXPasswordResponse{Password: f.password}, nil
}

func TestPasswordsComeFromTheAccountServerWhenAccountsAreKeptThere(t *testing.T) {
	globals.Logger = plogger.NewLogger()

	server := &fakeAccountServer{pid: 1800000000, password: "nex-password"}

	previousLocal, previousClient, previousMetadata := globals.LocalAccounts, globals.GRPCAccountClient, globals.GRPCAccountCommonMetadata
	globals.LocalAccounts, globals.GRPCAccountClient = false, server
	globals.GRPCAccountCommonMetadata = metadata.Pairs("X-API-Key", "account-key")

	t.Cleanup(func() {
		globals.LocalAccounts, globals.GRPCAccountClient, globals.GRPCAccountCommonMetadata = previousLocal, previousClient, previousMetadata
	})

	password, resultCode := globals.PasswordFromPID(1800000000)
	if password != "nex-password" || resultCode != 0 {
		t.Errorf("got password %q and result code %#x, want the account server's password", password, resultCode)
	}

	password, resultCode = globals.PasswordFromPID(1800000001)
	if password != "" || resultCode != nex.ResultCodes.RendezVous.InvalidUsername {
		t.Errorf("an account the server does not know gave password %q and result code %#x, want InvalidUsername", password, resultCode)
	}

	if len(server.keys) != 2 || server.keys[0] != "account-key" || server.keys[1] != "account-key" {
		t.Errorf("the account server was sent keys %q, want the API key with every request", server.keys)
	}
}
