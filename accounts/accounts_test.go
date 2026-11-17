package accounts_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/accounts"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

func openStore(t *testing.T) store.Store {
	t.Helper()

	if globals.Logger == nil {
		globals.Logger = plogger.NewLogger()
	}

	opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { opened.Close() })

	return opened
}

// * Expected values were made separately, with Python's hashlib:
// * sha256(pid as 4 little endian bytes + 02 65 43 46 + password)
func TestNintendoPasswordHash(t *testing.T) {
	tests := []struct {
		pid      uint32
		password string
		want     string
	}{
		{1800000000, "password", "19b280417a325fdc2bf7e91e6f0b4af3e544dd8cc88edfbd9fd3cc6d72e0d419"},
		{1, "hunter2", "33500470e509d7ab314f097ff83113f6afeef8fd1778de1c286f09b4da4de5f2"},
	}

	for _, test := range tests {
		if got := accounts.NintendoPasswordHash(test.pid, test.password); got != test.want {
			t.Errorf("hash of %q for PID %d = %s, want %s", test.password, test.pid, got, test.want)
		}
	}
}

func TestCreate(t *testing.T) {
	s := openStore(t)

	account, err := accounts.Create(s, "PlayerOne", "secret123", "")
	if err != nil {
		t.Fatal(err)
	}

	if account.PID != store.FirstPID || account.Username != "PlayerOne" || account.MiiName != "PlayerOne" || len(account.NEXPassword) != 16 {
		t.Errorf("got account %+v", account)
	}

	if err := accounts.CheckPassword(account, "secret123"); err != nil {
		t.Errorf("the right password was refused: %v", err)
	}

	// * A console sends the hash, not the password
	if err := accounts.CheckPasswordHash(account, accounts.NintendoPasswordHash(account.PID, "secret123")); err != nil {
		t.Errorf("the right password hash was refused: %v", err)
	}

	if err := accounts.CheckPassword(account, "secret124"); !errors.Is(err, accounts.ErrWrongPassword) {
		t.Errorf("a wrong password gave %v, want ErrWrongPassword", err)
	}

	// * The hash for another PID must not work, even with the right password
	if err := accounts.CheckPasswordHash(account, accounts.NintendoPasswordHash(account.PID+1, "secret123")); !errors.Is(err, accounts.ErrWrongPassword) {
		t.Errorf("another account's hash gave %v, want ErrWrongPassword", err)
	}

	_, err = accounts.Create(s, "playerone", "secret123", "")
	if !errors.Is(err, store.ErrUsernameTaken) {
		t.Errorf("creating a taken username gave %v, want ErrUsernameTaken", err)
	}
}

func TestStoredPasswordIsSaltedAndNotReversible(t *testing.T) {
	s := openStore(t)

	first, err := accounts.Create(s, "PlayerOne", "samepassword", "")
	if err != nil {
		t.Fatal(err)
	}

	consoleHash := accounts.NintendoPasswordHash(first.PID, "samepassword")

	if first.PasswordHash == "" || strings.Contains(first.PasswordHash, "samepassword") || strings.Contains(first.PasswordHash, consoleHash) {
		t.Fatalf("stored password hash %q holds the password or the hash a console sends", first.PasswordHash)
	}

	if !strings.HasPrefix(first.PasswordHash, "$2") {
		t.Errorf("stored password hash %q is not a bcrypt hash", first.PasswordHash)
	}

	// * Setting the same password again must store something
	// * different, as each stored hash has its own random salt
	if err := accounts.SetPassword(s, first.PID, "samepassword"); err != nil {
		t.Fatal(err)
	}

	again, err := s.GetAccountByPID(first.PID)
	if err != nil {
		t.Fatal(err)
	}

	if again.PasswordHash == first.PasswordHash {
		t.Error("the same password was stored the same way twice, so it is not salted")
	}

	if err := accounts.CheckPassword(again, "samepassword"); err != nil {
		t.Errorf("the password was refused after being set again: %v", err)
	}
}

func TestAccountWithoutPasswordCanNotLogIn(t *testing.T) {
	s := openStore(t)

	account, err := s.CreateAccount(store.NewAccount{Username: "NoPassword"})
	if err != nil {
		t.Fatal(err)
	}

	for _, password := range []string{"", "anything"} {
		if err := accounts.CheckPassword(account, password); !errors.Is(err, accounts.ErrWrongPassword) {
			t.Errorf("password %q on an account with no password gave %v, want ErrWrongPassword", password, err)
		}
	}

	if err := accounts.CheckPasswordHash(account, ""); !errors.Is(err, accounts.ErrWrongPassword) {
		t.Errorf("an empty hash on an account with no password gave %v, want ErrWrongPassword", err)
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	s := openStore(t)

	for _, username := range []string{"", "short", "seventeen_chars__", "has space", "emoji😀name", "semi;colon"} {
		if _, err := accounts.Create(s, username, "secret123", ""); !errors.Is(err, accounts.ErrInvalidUsername) {
			t.Errorf("username %q gave %v, want ErrInvalidUsername", username, err)
		}
	}

	for _, password := range []string{"", "short", "seventeen_chars__"} {
		if _, err := accounts.Create(s, "PlayerOne", password, ""); !errors.Is(err, accounts.ErrInvalidPassword) {
			t.Errorf("password %q gave %v, want ErrInvalidPassword", password, err)
		}
	}

	// * Nothing above should have created an account
	if _, err := s.GetAccountByPID(store.FirstPID); !errors.Is(err, store.ErrAccountNotFound) {
		t.Errorf("a rejected account was created anyway: %v", err)
	}

	for _, username := range []string{"sixchr", "sixteen_chars_ok", "dots.and-dash_1"} {
		if _, err := accounts.Create(s, username, "secret123", ""); err != nil {
			t.Errorf("username %q was refused: %v", username, err)
		}
	}
}

type ownedCourses map[types.PID]int

func (o ownedCourses) GetUserCourseObjectIDs(pid types.PID) (types.List[types.UInt64], *nex.Error) {
	ids := types.NewList[types.UInt64]()
	for i := range o[pid] {
		ids = append(ids, types.UInt64(i+1))
	}

	return ids, nil
}

func TestCreateForConsoleUsesTheConsolesPIDAndNoPassword(t *testing.T) {
	accountStore := openStore(t)

	account, err := accounts.CreateForConsole(accountStore, ownedCourses{}, "ConsolePlayer", 1712345678)
	if err != nil {
		t.Fatal(err)
	}

	if account.PID != 1712345678 || account.PasswordHash != "" || account.NEXPassword == "" {
		t.Errorf("got %+v, want the given PID, no password and a NEX password", account)
	}
}

func TestCreateForConsoleRefusesWhatItCanNotUse(t *testing.T) {
	accountStore := openStore(t)

	if _, err := accounts.CreateForConsole(accountStore, ownedCourses{1712345678: 2}, "ConsolePlayer", 1712345678); !errors.Is(err, accounts.ErrPIDHasCourses) {
		t.Errorf("a PID that owns courses gave %v, want ErrPIDHasCourses", err)
	}

	if _, err := accounts.CreateForConsole(accountStore, ownedCourses{}, "no", 1712345678); !errors.Is(err, accounts.ErrInvalidUsername) {
		t.Errorf("a bad username gave %v, want ErrInvalidUsername", err)
	}

	if _, err := accounts.CreateForConsole(accountStore, ownedCourses{}, "ConsolePlayer", 0); err == nil {
		t.Error("no PID was accepted")
	}

	if _, err := accountStore.GetAccountByUsername("ConsolePlayer"); err == nil {
		t.Error("an account was made by a refused request")
	}
}
