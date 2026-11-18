package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PretendoNetwork/super-mario-maker/accounts"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

// * Points the command at a new SQLite database, and returns where it is
func useDatabase(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "smm.db")

	t.Setenv("PN_SMM_DATABASE", "sqlite")
	t.Setenv("PN_SMM_SQLITE_PATH", path)
	t.Setenv("SMM_ACCOUNT_PASSWORD", "")

	return path
}

func openDatabase(t *testing.T, path string) *sqlite.Store {
	t.Helper()

	opened, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { opened.Close() })

	return opened
}

// * Runs the command with what is given on standard input, and returns
// * what it printed
func runWith(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()

	inputReader, inputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	outputReader, outputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		io.WriteString(inputWriter, input)
		inputWriter.Close()
	}()

	printed := make(chan string)
	go func() {
		data, _ := io.ReadAll(outputReader)
		printed <- string(data)
	}()

	previousInput, previousOutput := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inputReader, outputWriter

	runError := run(args)

	os.Stdin, os.Stdout = previousInput, previousOutput
	outputWriter.Close()
	inputReader.Close()

	return <-printed, runError
}

func TestTheAccountCommandShowsHowToUseItWhenGivenTheWrongArguments(t *testing.T) {
	useDatabase(t)

	for _, args := range [][]string{
		{},
		{"create"},
		{"create", "PlayerOne", "extra"},
		{"cemu", "PlayerOne"},
		{"console", "PlayerOne"},
		{"delete", "PlayerOne"},
	} {
		_, err := runWith(t, "", args...)
		if err == nil || err.Error() != usage {
			t.Errorf("%q: got %v, want the usage", args, err)
		}
	}
}

func TestTheAccountCommandRefusesADatabaseItDoesNotKnow(t *testing.T) {
	useDatabase(t)
	t.Setenv("PN_SMM_DATABASE", "mysql")

	_, err := runWith(t, "", "show", "PlayerOne")
	if err == nil || err.Error() != "PN_SMM_DATABASE is not a supported database. Expected postgres or sqlite, got mysql" {
		t.Errorf("got %v, want the database refused", err)
	}
}

func TestTheAccountCommandCreatesAnAccountWithThePasswordFromTheEnvironment(t *testing.T) {
	path := useDatabase(t)
	t.Setenv("SMM_ACCOUNT_PASSWORD", "secret123")

	printed, err := runWith(t, "", "create", "PlayerOne")
	if err != nil {
		t.Fatal(err)
	}

	account, err := openDatabase(t, path).GetAccountByUsername("PlayerOne")
	if err != nil {
		t.Fatal(err)
	}

	if printed != fmt.Sprintf("Created account PlayerOne with PID %d\n", account.PID) {
		t.Errorf("printed %q", printed)
	}

	if err := accounts.CheckPassword(account, "secret123"); err != nil {
		t.Errorf("the account does not have the password given: %v", err)
	}
}

func TestTheAccountCommandReadsThePasswordFromStandardInput(t *testing.T) {
	path := useDatabase(t)

	if _, err := runWith(t, "piped-secret\r\n", "create", "PlayerOne"); err != nil {
		t.Fatal(err)
	}

	account, err := openDatabase(t, path).GetAccountByUsername("PlayerOne")
	if err != nil {
		t.Fatal(err)
	}

	// * The line ending is not part of the password
	if err := accounts.CheckPassword(account, "piped-secret"); err != nil {
		t.Errorf("the account does not have the piped password: %v", err)
	}
}

func TestTheAccountCommandNeedsAPassword(t *testing.T) {
	path := useDatabase(t)

	_, err := runWith(t, "", "create", "PlayerOne")
	if err == nil || err.Error() != "no password given" {
		t.Errorf("got %v, want no password given", err)
	}

	if _, err := openDatabase(t, path).GetAccountByUsername("PlayerOne"); !errors.Is(err, store.ErrAccountNotFound) {
		t.Errorf("an account was made without a password: %v", err)
	}
}

func TestTheAccountCommandRefusesATakenUsername(t *testing.T) {
	useDatabase(t)
	t.Setenv("SMM_ACCOUNT_PASSWORD", "secret123")

	if _, err := runWith(t, "", "create", "PlayerOne"); err != nil {
		t.Fatal(err)
	}

	if _, err := runWith(t, "", "create", "PlayerOne"); !errors.Is(err, store.ErrUsernameTaken) {
		t.Errorf("got %v, want the username refused as taken", err)
	}
}

func TestTheAccountCommandRefusesAnInvalidUsernameOrPassword(t *testing.T) {
	useDatabase(t)

	t.Setenv("SMM_ACCOUNT_PASSWORD", "secret123")
	if _, err := runWith(t, "", "create", "P!"); !errors.Is(err, accounts.ErrInvalidUsername) {
		t.Errorf("got %v, want the username refused", err)
	}

	t.Setenv("SMM_ACCOUNT_PASSWORD", "short")
	if _, err := runWith(t, "", "create", "PlayerOne"); !errors.Is(err, accounts.ErrInvalidPassword) {
		t.Errorf("got %v, want the password refused", err)
	}
}

func TestTheAccountCommandChangesAPassword(t *testing.T) {
	path := useDatabase(t)
	t.Setenv("SMM_ACCOUNT_PASSWORD", "secret123")

	if _, err := runWith(t, "", "create", "PlayerOne"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SMM_ACCOUNT_PASSWORD", "another456")

	printed, err := runWith(t, "", "password", "PlayerOne")
	if err != nil {
		t.Fatal(err)
	}

	if printed != "Changed the password of account PlayerOne\n" {
		t.Errorf("printed %q", printed)
	}

	account, err := openDatabase(t, path).GetAccountByUsername("PlayerOne")
	if err != nil {
		t.Fatal(err)
	}

	if accounts.CheckPassword(account, "another456") != nil || accounts.CheckPassword(account, "secret123") == nil {
		t.Error("the account does not have only the new password")
	}

	// * An account that is not there is found out before the password is asked for
	t.Setenv("SMM_ACCOUNT_PASSWORD", "")
	if _, err := runWith(t, "", "password", "PlayerTwo"); !errors.Is(err, store.ErrAccountNotFound) {
		t.Errorf("got %v, want the account not found", err)
	}
}

func TestTheAccountCommandShowsAnAccountsPID(t *testing.T) {
	path := useDatabase(t)
	t.Setenv("SMM_ACCOUNT_PASSWORD", "secret123")

	if _, err := runWith(t, "", "create", "PlayerOne"); err != nil {
		t.Fatal(err)
	}

	printed, err := runWith(t, "", "show", "PlayerOne")
	if err != nil {
		t.Fatal(err)
	}

	account, _ := openDatabase(t, path).GetAccountByUsername("PlayerOne")
	want := fmt.Sprintf("PlayerOne has PID %d and was created %s\n", account.PID, account.CreatedAt.Format("2006-01-02"))

	if printed != want {
		t.Errorf("printed %q, want %q", printed, want)
	}

	if _, err := runWith(t, "", "show", "PlayerTwo"); !errors.Is(err, store.ErrAccountNotFound) {
		t.Errorf("got %v, want the account not found", err)
	}
}

func TestTheAccountCommandWritesACemuAccountFile(t *testing.T) {
	path := useDatabase(t)
	t.Setenv("SMM_ACCOUNT_PASSWORD", "secret123")

	if _, err := runWith(t, "", "create", "PlayerOne"); err != nil {
		t.Fatal(err)
	}

	account, _ := openDatabase(t, path).GetAccountByUsername("PlayerOne")

	// * Cemu's folders are made when they are not there yet
	file := filepath.Join(t.TempDir(), "mlc01", "usr", "save", "system", "act", "80000001", "account.dat")

	printed, err := runWith(t, "", "cemu", "PlayerOne", file)
	if err != nil {
		t.Fatal(err)
	}

	if printed != "Wrote "+file+" for account PlayerOne. It has an empty Mii, as there was no file from Cemu to keep one from\n" {
		t.Errorf("printed %q", printed)
	}

	contents, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{"AccountId=PlayerOne", fmt.Sprintf("PrincipalId=%x", account.PID), "AccountPasswordCache=" + accounts.NintendoPasswordHash(account.PID, "secret123")} {
		if !strings.Contains(string(contents), line+"\n") {
			t.Errorf("the file does not hold %q:\n%s", line, contents)
		}
	}

	// * It holds what is needed to log in, so only its owner may read it
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Errorf("the file can be read by others: %v", info.Mode().Perm())
	}

	printed, err = runWith(t, "", "cemu", "PlayerOne", file)
	if err != nil {
		t.Fatal(err)
	}

	if printed != "Updated "+file+" to log in as account PlayerOne\n" {
		t.Errorf("the second time printed %q", printed)
	}
}

func TestTheAccountCommandOnlyWritesACemuFileWithTheRightPassword(t *testing.T) {
	useDatabase(t)
	t.Setenv("SMM_ACCOUNT_PASSWORD", "secret123")

	if _, err := runWith(t, "", "create", "PlayerOne"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SMM_ACCOUNT_PASSWORD", "wrong-password")

	file := filepath.Join(t.TempDir(), "account.dat")

	if _, err := runWith(t, "", "cemu", "PlayerOne", file); !errors.Is(err, accounts.ErrWrongPassword) {
		t.Errorf("got %v, want the password refused", err)
	}

	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Error("a file was written with the wrong password")
	}
}

func TestTheAccountCommandCreatesAnAccountForAConsole(t *testing.T) {
	path := useDatabase(t)

	printed, err := runWith(t, "", "console", "WiiUPlayer", "1800000123")
	if err != nil {
		t.Fatal(err)
	}

	account, err := openDatabase(t, path).GetAccountByUsername("WiiUPlayer")
	if err != nil {
		t.Fatal(err)
	}

	want := fmt.Sprintf("Created account WiiUPlayer for the console with PID 1800000123\npid=1800000123\nnex_password=%s\n", account.NEXPassword)
	if account.PID != 1800000123 || printed != want {
		t.Errorf("made PID %d and printed %q, want PID 1800000123 and %q", account.PID, printed, want)
	}

	if _, err := runWith(t, "", "console", "WiiUPlayer2", "1800000123"); err == nil || err.Error() != "an account with PID 1800000123 already exists" {
		t.Errorf("a taken PID gave %v", err)
	}
}

func TestTheAccountCommandRefusesAPlayerIDThatIsNotOne(t *testing.T) {
	useDatabase(t)

	for _, pid := range []string{"0", "-5", "player", "4294967296"} {
		_, err := runWith(t, "", "console", "WiiUPlayer", pid)
		if err == nil || err.Error() != fmt.Sprintf("%q is not a player ID", pid) {
			t.Errorf("PID %s gave %v", pid, err)
		}
	}
}
