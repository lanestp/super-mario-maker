package accounts_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/PretendoNetwork/super-mario-maker/accounts"
)

// * Reads an account file the way Cemu does: one key=value per line,
// * where a later line wins and lines without an = are ignored
func parseCemuAccountFile(contents []byte) map[string]string {
	values := map[string]string{}

	for _, line := range strings.Split(string(contents), "\n") {
		if key, value, found := strings.Cut(line, "="); found {
			values[key] = value
		}
	}

	return values
}

func TestCemuAccountFileNew(t *testing.T) {
	s := openStore(t)

	account, err := accounts.Create(s, "PlayerOne", "secret123", "Player")
	if err != nil {
		t.Fatal(err)
	}

	contents, err := accounts.CemuAccountFile(nil, account, "secret123")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(string(contents), "AccountInstance_00000000\n") {
		t.Errorf("the file does not start with the Cemu account header:\n%s", contents)
	}

	values := parseCemuAccountFile(contents)

	if values["AccountId"] != "PlayerOne" || values["IsPasswordCacheEnabled"] != "1" {
		t.Errorf("got account ID %q and password cache enabled %q", values["AccountId"], values["IsPasswordCacheEnabled"])
	}

	// * Cemu reads the principal ID as hexadecimal, and refuses to go online when it is 0
	pid, err := strconv.ParseUint(values["PrincipalId"], 16, 32)
	if err != nil || uint32(pid) != account.PID {
		t.Errorf("got principal ID %q, want %x", values["PrincipalId"], account.PID)
	}

	if values["AccountPasswordCache"] != accounts.NintendoPasswordHash(account.PID, "secret123") {
		t.Errorf("got password cache %q, which is not the hash a console sends", values["AccountPasswordCache"])
	}

	// * Cemu refuses to load a file where these are not exactly this long
	for key, length := range map[string]int{"Uuid": 32, "MiiData": 192, "MiiName": 44, "AccountPasswordCache": 64} {
		if len(values[key]) != length {
			t.Errorf("%s is %d characters, Cemu needs %d", key, len(values[key]), length)
		}
	}

	// * "Player" in UTF-16, then zeros
	if !strings.HasPrefix(values["MiiName"], "0050006c0061007900650072"+"0000") {
		t.Errorf("got Mii name %q", values["MiiName"])
	}

	if strings.Contains(string(contents), "secret123") {
		t.Error("the file holds the password itself")
	}
}

func TestCemuAccountFileKeepsExistingSettings(t *testing.T) {
	s := openStore(t)

	account, err := accounts.Create(s, "PlayerOne", "secret123", "")
	if err != nil {
		t.Fatal(err)
	}

	miiData := strings.Repeat("ab", 96)

	// * Like the file Cemu makes for itself, which is offline: it
	// * has a Mii, and has no account ID or password
	existing := "AccountInstance_00000000\r\n" +
		"PersistentId=80000001\r\n" +
		"TransferableIdBase=2000004\r\n" +
		"Uuid=00112233445566778899aabbccddeeff\r\n" +
		"MiiData=" + miiData + "\r\n" +
		"MiiName=00430065006d0075000000000000000000000000000\r\n" +
		"AccountId=\r\n" +
		"BirthYear=7b8\r\n" +
		"PrincipalId=0\r\n" +
		"IsPasswordCacheEnabled=0\r\n" +
		"AccountPasswordCache=" + strings.Repeat("00", 32) + "\r\n" +
		"SomethingCemuDoesNotKnow=kept\r\n\r\n"

	contents, err := accounts.CemuAccountFile([]byte(existing), account, "secret123")
	if err != nil {
		t.Fatal(err)
	}

	values := parseCemuAccountFile(contents)

	kept := map[string]string{
		"PersistentId":             "80000001",
		"TransferableIdBase":       "2000004",
		"Uuid":                     "00112233445566778899aabbccddeeff",
		"MiiData":                  miiData,
		"BirthYear":                "7b8",
		"SomethingCemuDoesNotKnow": "kept",
	}

	for key, want := range kept {
		if values[key] != want {
			t.Errorf("%s changed to %q, want it kept as %q", key, values[key], want)
		}
	}

	if values["AccountId"] != "PlayerOne" || values["IsPasswordCacheEnabled"] != "1" || values["AccountPasswordCache"] != accounts.NintendoPasswordHash(account.PID, "secret123") {
		t.Errorf("the login settings were not updated: %v", values)
	}

	if pid, _ := strconv.ParseUint(values["PrincipalId"], 16, 32); uint32(pid) != account.PID {
		t.Errorf("got principal ID %q, want %x", values["PrincipalId"], account.PID)
	}

	for _, key := range []string{"AccountId", "PrincipalId", "IsPasswordCacheEnabled", "AccountPasswordCache", "MiiData"} {
		if count := strings.Count(string(contents), "\n"+key+"="); count != 1 {
			t.Errorf("%s is in the file %d times, want once", key, count)
		}
	}

	// * Making the file again from its own output changes nothing
	again, err := accounts.CemuAccountFile(contents, account, "secret123")
	if err != nil {
		t.Fatal(err)
	}

	if string(again) != string(contents) {
		t.Errorf("making the file a second time changed it:\n%s\n---\n%s", contents, again)
	}
}

// * A Mii name holds at most 10 characters, and the 11th is always 0
func TestCemuAccountFileCutsALongMiiNameTo10Characters(t *testing.T) {
	s := openStore(t)

	account, err := accounts.Create(s, "PlayerOne", "secret123", "ABCDEFGHIJKL")
	if err != nil {
		t.Fatal(err)
	}

	contents, err := accounts.CemuAccountFile(nil, account, "secret123")
	if err != nil {
		t.Fatal(err)
	}

	// * "ABCDEFGHIJ" in UTF-16, then a 0
	want := "004100420043004400450046004700480049004a" + "0000"

	if got := parseCemuAccountFile(contents)["MiiName"]; got != want {
		t.Errorf("got Mii name %q, want %q", got, want)
	}
}

// * Each character takes 4 hexadecimal digits, so U+FFFF is the last one
// * that fits. Anything past it is written as 0
func TestCemuAccountFileKeepsMiiNameCharactersUpToFFFF(t *testing.T) {
	s := openStore(t)

	account, err := accounts.Create(s, "PlayerOne", "secret123", "A￿\U0001F600B")
	if err != nil {
		t.Fatal(err)
	}

	contents, err := accounts.CemuAccountFile(nil, account, "secret123")
	if err != nil {
		t.Fatal(err)
	}

	want := "0041" + "ffff" + "0000" + "0042" + strings.Repeat("0000", 7)

	if got := parseCemuAccountFile(contents)["MiiName"]; got != want {
		t.Errorf("got Mii name %q, want %q", got, want)
	}
}

func TestCemuAccountFileNeedsTheRightPassword(t *testing.T) {
	s := openStore(t)

	account, err := accounts.Create(s, "PlayerOne", "secret123", "")
	if err != nil {
		t.Fatal(err)
	}

	contents, err := accounts.CemuAccountFile(nil, account, "wrongpass")
	if !errors.Is(err, accounts.ErrWrongPassword) || contents != nil {
		t.Errorf("a wrong password gave %v and a file of %d bytes, want ErrWrongPassword and no file", err, len(contents))
	}
}
