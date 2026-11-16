package accounts

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/PretendoNetwork/super-mario-maker/store"
)

// * The first line of a Cemu account file
const cemuAccountHeader = "AccountInstance_00000000"

// CemuAccountFile returns the contents of a Cemu account.dat file which
// logs in as the given account. The password is needed as the file holds
// the hash a console sends in place of it, and must be the account's
//
// When existing holds the contents of an account.dat file, such as the one
// Cemu makes for itself, only the settings to do with logging in are
// changed and everything else, like the Mii, is kept. When it is empty a
// new file is made, with an empty Mii
func CemuAccountFile(existing []byte, account store.Account, password string) ([]byte, error) {
	if err := CheckPassword(account, password); err != nil {
		return nil, err
	}

	login := []struct{ key, value string }{
		{"AccountId", account.Username},
		{"PrincipalId", fmt.Sprintf("%x", account.PID)},
		{"IsPasswordCacheEnabled", "1"},
		{"AccountPasswordCache", NintendoPasswordHash(account.PID, password)},
	}

	var lines []string

	if len(strings.TrimSpace(string(existing))) == 0 {
		uuid := make([]byte, 16)
		if _, err := rand.Read(uuid); err != nil {
			return nil, err
		}

		// * A Mii name is 11 UTF-16 characters, written as 4 hexadecimal digits each
		miiName := []rune(account.MiiName)
		if len(miiName) > 10 {
			miiName = miiName[:10]
		}

		var encodedMiiName strings.Builder
		for i := range 11 {
			character := rune(0)
			if i < len(miiName) && miiName[i] <= 0xFFFF {
				character = miiName[i]
			}

			fmt.Fprintf(&encodedMiiName, "%04x", character)
		}

		lines = []string{
			cemuAccountHeader,
			"PersistentId=80000001",
			"TransferableIdBase=0",
			"Uuid=" + hex.EncodeToString(uuid),
			"MiiData=" + strings.Repeat("00", 96),
			"MiiName=" + encodedMiiName.String(),
			"BirthYear=7d0",
			"BirthMonth=1",
			"BirthDay=1",
			"Gender=0",
			"EmailAddress=",
			"Country=0",
			"SimpleAddressId=0",
		}
	} else {
		lines = strings.Split(strings.ReplaceAll(string(existing), "\r\n", "\n"), "\n")

		// * Trailing empty lines are dropped, one is added back at the end
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
	}

	for _, setting := range login {
		replaced := false

		for i, line := range lines {
			if key, _, found := strings.Cut(line, "="); found && key == setting.key {
				lines[i] = setting.key + "=" + setting.value
				replaced = true
			}
		}

		if !replaced {
			lines = append(lines, setting.key+"="+setting.value)
		}
	}

	return []byte(strings.Join(lines, "\n") + "\n"), nil
}
