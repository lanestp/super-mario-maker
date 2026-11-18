package bundle_test

import (
	"archive/zip"
	"bytes"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/accounts"
	"github.com/PretendoNetwork/super-mario-maker/bundle"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/mii"
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

func unzip(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()

	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{}

	for _, file := range reader.File {
		opened, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}

		data, err := io.ReadAll(opened)
		opened.Close()

		if err != nil {
			t.Fatal(err)
		}

		files[file.Name] = data
	}

	return files
}

func accountFileValues(data []byte) map[string]string {
	values := map[string]string{}

	for _, line := range strings.Split(string(data), "\n") {
		if key, value, found := strings.Cut(line, "="); found {
			values[key] = value
		}
	}

	return values
}

func TestCemuBundleLogsInAsThePlayer(t *testing.T) {
	accountStore := openStore(t)

	account, err := accounts.Create(accountStore, "CemuPlayer01", "a-password", "")
	if err != nil {
		t.Fatal(err)
	}

	archive, err := bundle.Cemu(account, "a-password", "https://smm.example.com")
	if err != nil {
		t.Fatal(err)
	}

	files := unzip(t, archive)

	values := accountFileValues(files["mlc01/usr/save/system/act/80000001/account.dat"])
	if values["AccountId"] != "CemuPlayer01" || values["PrincipalId"] != "6b49d200" || values["IsPasswordCacheEnabled"] != "1" {
		t.Errorf("the account file says %v", values)
	}

	// * What Cemu sends in place of a password must be one the server takes
	if err := accounts.CheckPasswordHash(account, values["AccountPasswordCache"]); err != nil {
		t.Errorf("the server refuses the account file's login: %v", err)
	}

	miiData, err := hex.DecodeString(values["MiiData"])
	if err != nil || len(miiData) != mii.Size {
		t.Fatalf("the Mii is %d bytes (%v), want %d", len(miiData), err, mii.Size)
	}

	if binary.BigEndian.Uint16(miiData[mii.Size-2:]) != mii.Checksum(miiData[:mii.Size-2]) {
		t.Error("the Mii's checksum is wrong, so a game would refuse it")
	}

	if services := string(files["network_services.xml"]); !strings.Contains(services, "<act>https://smm.example.com</act>") {
		t.Errorf("Cemu is not pointed at the server:\n%s", services)
	}

	if services := string(files["network_services.xml"]); !strings.Contains(services, "<olv>https://smm.example.com/olv/v1/endpoint</olv>") {
		t.Errorf("Cemu is not pointed at the server's Miiverse stand-in, so uploads end in error 115-1101:\n%s", services)
	}

	if len(files["otp.bin"]) != 1024 || len(files["seeprom.bin"]) != 512 || !bytes.Equal(files["otp.bin"], make([]byte, 1024)) {
		t.Error("otp.bin and seeprom.bin are not blank files of the sizes Cemu wants")
	}

	certificates := 0

	for name, data := range files {
		if !strings.Contains(name, "/scerts/") {
			continue
		}

		certificates++

		// * Cemu refuses to go online if any of these does not read as a certificate
		if _, err := x509.ParseCertificate(data); err != nil {
			t.Errorf("%s does not read as a certificate: %v", name, err)
		}
	}

	if certificates != 39 {
		t.Errorf("the bundle has %d certificates, want 39", certificates)
	}

	if !strings.Contains(string(files["README.txt"]), "CemuPlayer01") {
		t.Error("the README does not name the account to pick")
	}
}

func TestCemuBundleNeedsTheAccountsPassword(t *testing.T) {
	accountStore := openStore(t)

	account, err := accounts.Create(accountStore, "CemuPlayer01", "a-password", "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := bundle.Cemu(account, "not-the-password", "https://smm.example.com"); err == nil {
		t.Error("a bundle was made with the wrong password, which could never log in")
	}
}

func TestWiiUBundleHoldsThePluginAndThePlayersSettings(t *testing.T) {
	account := store.Account{PID: 1712345678, Username: "ConsolePlayer", NEXPassword: "abcdefghijklmnop"}
	plugin := []byte("the plugin")

	archive, err := bundle.WiiU(account, "203.0.113.4", 6004, plugin)
	if err != nil {
		t.Fatal(err)
	}

	files := unzip(t, archive)

	if !bytes.Equal(files["wiiu/environments/aroma/plugins/smm-private-server.wps"], plugin) {
		t.Error("the plugin is not where Aroma loads plugins from")
	}

	// * The plugin reads this exact file for the player with this ID
	settings := string(files["wiiu/smm-private-server/1712345678.txt"])
	if settings != "host=203.0.113.4\nport=6004\npassword=abcdefghijklmnop\n" {
		t.Errorf("the settings file is %q", settings)
	}

	if !strings.Contains(string(files["README.txt"]), "1712345678") {
		t.Error("the README does not say which player ID it is for")
	}
}

func TestWiiUBundleRefusesWhatThePluginCouldNotUse(t *testing.T) {
	account := store.Account{PID: 1712345678, NEXPassword: "abcdefghijklmnop"}

	if _, err := bundle.WiiU(account, "smm.example.com", 6004, []byte("plugin")); err == nil {
		t.Error("a host name was accepted. The game only has room for an IPv4 address")
	}

	if _, err := bundle.WiiU(account, "203.0.113.4", 6004, nil); err == nil {
		t.Error("a bundle with no plugin was made")
	}
}
