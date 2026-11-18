// Package bundle makes the files a player unpacks to join the server:
// for the Cemu emulator, or for a Wii U that keeps using another network
// for everything but Super Mario Maker.
//
// Nothing in a bundle comes from a real console. What Cemu wants from
// one is made up here: blank files, and dummy certificates that are only
// there because Cemu refuses to go online without something that reads
// as a certificate in each place
package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/PretendoNetwork/super-mario-maker/accountapi"
	"github.com/PretendoNetwork/super-mario-maker/accounts"
	"github.com/PretendoNetwork/super-mario-maker/mii"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

// * The Mii Cemu gives a new account: every part of the face set the same
// * way. It is renamed for each player
const placeholderMii = "010001100000d73e030034330100010001000100010001000100640065006600610075006c0074000000000000000100010001000100010001000106010001000100010001000100010001000100010001000100010001000100010001000100"

const (
	certificateFolder = "mlc01/sys/title/0005001b/10054000/content"
	accountFile       = "mlc01/usr/save/system/act/80000001/account.dat"

	// PluginPath is where the plugin goes on a Wii U's SD card
	PluginPath = "wiiu/environments/aroma/plugins/smm-private-server.wps"
	// * Where the plugin looks for a player's settings
	pluginSettingsFolder = "wiiu/smm-private-server"
)

// * The authorities a console trusts, by the names Cemu looks for. Each
// * gets a dummy certificate
var serverCertificates = strings.Fields(`
ADDTRUST_EXT_CA_ROOT BALTIMORE_CYBERTRUST_ROOT_CA CACERT_NINTENDO_CA_G2 CACERT_NINTENDO_CA_G3
CACERT_NINTENDO_CA CACERT_NINTENDO_CLASS2_CA_G2 CACERT_NINTENDO_CLASS2_CA_G3
CACERT_NINTENDO_CLASS2_CA COMODO_CA CYBERTRUST_GLOBAL_ROOT_CA DIGICERT_ASSURED_ID_ROOT_CA_G2
DIGICERT_ASSURED_ID_ROOT_CA DIGICERT_GLOBAL_ROOT_CA_G2 DIGICERT_GLOBAL_ROOT_CA
DIGICERT_HIGH_ASSURANCE_EV_ROOT_CA ENTRUST_CA_2048 ENTRUST_ROOT_CA_G2 ENTRUST_ROOT_CA
ENTRUST_SECURE_SERVER_CA EQUIFAX_SECURE_CA GEOTRUST_GLOBAL_CA GEOTRUST_GLOBAL_CA2
GEOTRUST_PRIMARY_CA_G3 GEOTRUST_PRIMARY_CA GLOBALSIGN_ROOT_CA_R2 GLOBALSIGN_ROOT_CA_R3
GLOBALSIGN_ROOT_CA GTE_CYBERTRUST_GLOBAL_ROOT THAWTE_PREMIUM_SERVER_CA
THAWTE_PRIMARY_ROOT_CA_G3 THAWTE_PRIMARY_ROOT_CA UTN_DATACORP_SGC_CA
UTN_USERFIRST_HARDWARE_CA VERISIGN_CLASS3_PUBLIC_PRIMARY_CA_G2
VERISIGN_CLASS3_PUBLIC_PRIMARY_CA_G3 VERISIGN_CLASS3_PUBLIC_PRIMARY_CA_G5
VERISIGN_CLASS3_PUBLIC_PRIMARY_CA VERISIGN_UNIVERSAL_ROOT_CA VERIZON_GLOBAL_ROOT_CA
`)

// * A console's own certificates and keys. These can not be made up, as
// * the keys are locked with a key only that console has. One byte files
// * are enough for this server
var clientCertificates = []string{
	"WIIU_ACCOUNT_1_CERT.der", "WIIU_ACCOUNT_1_RSA_KEY.aes",
	"WIIU_COMMON_1_CERT.der", "WIIU_COMMON_1_RSA_KEY.aes",
	"WIIU_OLIVE_1_CERT.der", "WIIU_OLIVE_1_RSA_KEY.aes",
	"WIIU_VINO_1_CERT.der", "WIIU_VINO_1_RSA_KEY.aes",
	"WIIU_WOOD_1_CERT.der", "WIIU_WOOD_1_RSA_KEY.aes",
}

// * The services a console reaches besides the account server and
// * Miiverse. None are served, but Cemu wants an address for each
var unservedServices = []string{"ecs", "nus", "ias", "ccsu", "ccs", "idbe", "boss", "tagaya"}

// Cemu returns the zip a player unpacks into Cemu's folder to play as
// the account. password is the account's, and accountServer is how Cemu
// reaches the account API, such as https://smm.example.com
func Cemu(account store.Account, password, accountServer string) ([]byte, error) {
	if err := accounts.CheckPassword(account, password); err != nil {
		return nil, err
	}

	files := map[string][]byte{
		"README.txt":           []byte(strings.ReplaceAll(cemuReadme, "{username}", account.Username)),
		"network_services.xml": []byte(networkServices(accountServer)),
		"otp.bin":              make([]byte, 1024),
		"seeprom.bin":          make([]byte, 512),
	}

	accountData, err := cemuAccountFile(account, password)
	if err != nil {
		return nil, err
	}

	files[accountFile] = accountData

	certificates, err := dummyCertificates()
	if err != nil {
		return nil, err
	}

	for name, data := range certificates {
		files[certificateFolder+"/scerts/"+name+".der"] = data
	}

	for _, name := range clientCertificates {
		files[certificateFolder+"/ccerts/"+name] = []byte{0}
	}

	return zipFiles(files)
}

// WiiU returns the zip a player unpacks onto a Wii U's SD card, so that
// Super Mario Maker, and nothing else, is played on this server. host
// and port are where the game server is, and plugin is the built plugin
func WiiU(account store.Account, host string, port int, plugin []byte) ([]byte, error) {
	if len(plugin) == 0 {
		return nil, fmt.Errorf("bundle: there is no plugin to put in the bundle")
	}

	// * The game is told the address in a field with room for an IPv4
	// * address, so a host name would not reach it
	if ip := net.ParseIP(host); ip == nil || ip.To4() == nil || strings.Contains(host, ":") {
		return nil, fmt.Errorf("bundle: the game server host %q is not an IPv4 address", host)
	}

	settings := fmt.Sprintf("host=%s\nport=%d\npassword=%s\n", host, port, account.NEXPassword)

	return zipFiles(map[string][]byte{
		"README.txt": []byte(strings.ReplaceAll(wiiUReadme, "{pid}", fmt.Sprint(account.PID))),
		PluginPath:   plugin,
		fmt.Sprintf("%s/%d.txt", pluginSettingsFolder, account.PID): []byte(settings),
	})
}

func cemuAccountFile(account store.Account, password string) ([]byte, error) {
	uuid := make([]byte, 16)
	if _, err := rand.Read(uuid); err != nil {
		return nil, err
	}

	placeholder, err := hex.DecodeString(placeholderMii)
	if err != nil {
		return nil, err
	}

	// * Written big endian, in 11 UTF-16 characters
	units := utf16.Encode([]rune(account.Username))
	units = units[:min(len(units), mii.NameLength)]
	name := make([]byte, 22)

	for i, unit := range units {
		name[i*2] = byte(unit >> 8)
		name[i*2+1] = byte(unit)
	}

	lines := []string{
		"AccountInstance_20120705",
		"PersistentId=80000001",
		"TransferableIdBase=2000004" + hex.EncodeToString(uuid[12:]),
		"Uuid=" + hex.EncodeToString(uuid),
		"MiiData=" + hex.EncodeToString(mii.Renamed(placeholder, account.Username, account.PID)),
		"MiiName=" + hex.EncodeToString(name),
		"AccountId=" + account.Username,
		"BirthYear=0",
		"BirthMonth=0",
		"BirthDay=0",
		"Gender=0",
		"EmailAddress=",
		"Country=0",
		"SimpleAddressId=0",
		fmt.Sprintf("PrincipalId=%x", account.PID),
		"IsPasswordCacheEnabled=1",
		"AccountPasswordCache=" + accounts.NintendoPasswordHash(account.PID, password),
	}

	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func networkServices(accountServer string) string {
	var urls strings.Builder

	fmt.Fprintf(&urls, "\t\t<act>%s</act>\n", accountServer)
	fmt.Fprintf(&urls, "\t\t<olv>%s%s/v1/endpoint</olv>\n", accountServer, accountapi.MiiversePath)

	for _, name := range unservedServices {
		fmt.Fprintf(&urls, "\t\t<%s>%s/unserved/%s</%s>\n", name, accountServer, name, name)
	}

	// * Cemu can only check the account server against a real console's
	// * certificates, which a player does not have. The connection is
	// * still encrypted
	return "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<content>\n\t<networkname>SMM</networkname>\n" +
		"\t<disablesslverification>1</disablesslverification>\n\t<urls>\n" + urls.String() + "\t</urls>\n</content>\n"
}

// * Self signed certificates that vouch for nothing. Cemu only needs each
// * to read as a certificate. One key signs all of them, as it is thrown
// * away straight after
func dummyCertificates() (map[string][]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	certificates := make(map[string][]byte, len(serverCertificates))

	for i, name := range serverCertificates {
		template := &x509.Certificate{
			SerialNumber:          big.NewInt(int64(i + 1)),
			Subject:               pkix.Name{Organization: []string{"Placeholder, not a real authority"}, CommonName: "Placeholder for " + name},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().AddDate(10, 0, 0),
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign,
		}

		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			return nil, err
		}

		certificates[name] = der
	}

	return certificates, nil
}

func zipFiles(files map[string][]byte) ([]byte, error) {
	var archive bytes.Buffer

	writer := zip.NewWriter(&archive)

	for name, data := range files {
		file, err := writer.Create(name)
		if err != nil {
			return nil, err
		}

		if _, err := file.Write(data); err != nil {
			return nil, err
		}
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}

	return archive.Bytes(), nil
}

const cemuReadme = `Joining the Super Mario Maker server with Cemu
===============================================

You need Cemu 2 and your own copy of Super Mario Maker for Wii U, installed
in Cemu with its update.

1. Start Cemu once, so that it makes its folders, and close it again.

2. Open Cemu's data folder. This is the folder that holds settings.xml and
   a folder named mlc01. In Cemu, File > Open Cemu folder takes you there.

3. Copy everything from this zip into that folder, so that the mlc01
   folder here merges with the one there. Replace files when asked.

4. Start Cemu. Open Options > General settings > Account. Pick the account
   named {username}, and set its network service to Custom. Close the
   settings.

5. Start the game and go into Course World.

Good to know

- Makers show without a face. Cemu can not draw Mii faces without files
  from a real Wii U.
- Uploading a course ends with error 115-1101. The course has been
  uploaded all the same: the error is the game failing to post to
  Miiverse, which does not exist here.
- This zip lets whoever has it play as you. Do not pass it on.

Nothing in this zip comes from a real console. otp.bin and seeprom.bin are
blank, and the certificates are dummies that were made for this zip.
`

const wiiUReadme = `Playing Super Mario Maker on the private server with a Wii U
==========================================================

Your console keeps using the network it is signed in to, such as
Pretendo, for everything else. Only Super Mario Maker is sent to the
private server.

You need a Wii U running Aroma, signed in to the network account with
player ID {pid}. On any other account on the console, Super Mario Maker
plays as before.

1. Copy the wiiu folder in this zip onto the root of your SD card. It
   holds the plugin, and a settings file for your account.

2. Start the console and Super Mario Maker, and go into Course World.

To stop playing on the private server, delete smm-private-server.wps from
sd:/wiiu/environments/aroma/plugins.

The settings file holds your password for the private server. Do not
pass it on.
`
