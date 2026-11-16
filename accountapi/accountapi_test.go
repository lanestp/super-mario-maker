package accountapi_test

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/accountapi"
	"github.com/PretendoNetwork/super-mario-maker/accounts"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

type fixture struct {
	t       *testing.T
	store   store.Store
	server  *httptest.Server
	account store.Account
	now     time.Time
}

const password = "secret123"

func newFixture(t *testing.T) *fixture {
	t.Helper()

	if globals.Logger == nil {
		globals.Logger = plogger.NewLogger()
	}

	opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { opened.Close() })

	f := &fixture{t: t, store: opened, now: time.Now()}

	f.account, err = accounts.Create(opened, "PlayerOne", password, "")
	if err != nil {
		t.Fatal(err)
	}

	handler, err := accountapi.Handler(accountapi.Config{
		Accounts:       opened,
		GameServerHost: "203.0.113.7",
		GameServerPort: 6004,
		GameServerIDs:  []string{"1018DB00"},
		Now:            func() time.Time { return f.now },
		LogError:       func(err error) { t.Errorf("unexpected server error: %v", err) },
	})
	if err != nil {
		t.Fatal(err)
	}

	f.server = httptest.NewServer(handler)
	t.Cleanup(f.server.Close)

	return f
}

type response struct {
	status int
	body   string
}

// * Finds the text of the first element with the given name, anywhere in the document
func (r response) field(t *testing.T, name string) string {
	t.Helper()

	decoder := xml.NewDecoder(strings.NewReader(r.body))

	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}

		if start, ok := token.(xml.StartElement); ok && start.Name.Local == name {
			var text string
			if err := decoder.DecodeElement(&text, &start); err != nil {
				t.Fatalf("reading <%s> from %s: %v", name, r.body, err)
			}

			return text
		}
	}
}

func (r response) root(t *testing.T) string {
	t.Helper()

	decoder := xml.NewDecoder(strings.NewReader(r.body))

	for {
		token, err := decoder.Token()
		if err != nil {
			t.Fatalf("response is not XML: %q", r.body)
		}

		if start, ok := token.(xml.StartElement); ok {
			return start.Name.Local
		}
	}
}

func (f *fixture) do(request *http.Request) response {
	f.t.Helper()

	result, err := f.server.Client().Do(request)
	if err != nil {
		f.t.Fatal(err)
	}

	defer result.Body.Close()

	body, err := io.ReadAll(result.Body)
	if err != nil {
		f.t.Fatal(err)
	}

	return response{status: result.StatusCode, body: string(body)}
}

func (f *fixture) post(form url.Values) response {
	f.t.Helper()

	request, err := http.NewRequest(http.MethodPost, f.server.URL+"/v1/api/oauth20/access_token/generate", strings.NewReader(form.Encode()))
	if err != nil {
		f.t.Fatal(err)
	}

	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return f.do(request)
}

func (f *fixture) get(path, accessToken string) response {
	f.t.Helper()

	request, err := http.NewRequest(http.MethodGet, f.server.URL+path, nil)
	if err != nil {
		f.t.Fatal(err)
	}

	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}

	return f.do(request)
}

// * Logs in the way a console does, sending a hash of the password
func (f *fixture) loginAs(username, password string, pid uint32) response {
	f.t.Helper()

	return f.post(url.Values{
		"grant_type":    {"password"},
		"user_id":       {username},
		"password":      {accounts.NintendoPasswordHash(pid, password)},
		"password_type": {"hash"},
	})
}

func (f *fixture) login() (accessToken, refreshToken string) {
	f.t.Helper()

	result := f.loginAs("PlayerOne", password, f.account.PID)
	if result.status != http.StatusOK {
		f.t.Fatalf("login failed with status %d: %s", result.status, result.body)
	}

	return result.field(f.t, "token"), result.field(f.t, "refresh_token")
}

func mustBeError(t *testing.T, result response, status int, code, what string) {
	t.Helper()

	if result.status != status || result.root(t) != "errors" || result.field(t, "code") != code {
		t.Errorf("%s: got status %d and body %s, want status %d with error code %s", what, result.status, result.body, status, code)
	}
}

func TestLogin(t *testing.T) {
	f := newFixture(t)

	result := f.loginAs("PlayerOne", password, f.account.PID)

	if result.status != http.StatusOK || result.root(t) != "OAuth20" {
		t.Fatalf("got status %d and body %s", result.status, result.body)
	}

	accessToken, refreshToken := result.field(t, "token"), result.field(t, "refresh_token")

	if len(accessToken) != 32 || len(refreshToken) != 40 || accessToken == refreshToken {
		t.Errorf("got access token %q and refresh token %q", accessToken, refreshToken)
	}

	if result.field(t, "expires_in") != "3600" {
		t.Errorf("got expires_in %q, want 3600", result.field(t, "expires_in"))
	}

	// * The username is not case sensitive
	if again := f.loginAs("playerone", password, f.account.PID); again.status != http.StatusOK {
		t.Errorf("login with a differently cased username gave status %d: %s", again.status, again.body)
	} else if again.field(t, "token") == accessToken {
		t.Error("two logins were given the same access token")
	}

	// * The password itself also works, for clients which do not hash it
	plain := f.post(url.Values{"grant_type": {"password"}, "user_id": {"PlayerOne"}, "password": {password}})
	if plain.status != http.StatusOK {
		t.Errorf("login with the password itself gave status %d: %s", plain.status, plain.body)
	}
}

func TestLoginRefused(t *testing.T) {
	f := newFixture(t)

	wrongPassword := f.loginAs("PlayerOne", "wrongpass", f.account.PID)
	mustBeError(t, wrongPassword, http.StatusBadRequest, "0106", "wrong password")

	unknownAccount := f.loginAs("NoSuchUser", password, f.account.PID)
	mustBeError(t, unknownAccount, http.StatusBadRequest, "0106", "unknown account")

	// * Nothing in the answer may show which of the two was wrong
	if wrongPassword.body != unknownAccount.body {
		t.Errorf("a wrong password and an unknown account are answered differently:\n%s\n%s", wrongPassword.body, unknownAccount.body)
	}

	// * The right password hashed for another account's PID is not this account's hash
	mustBeError(t, f.loginAs("PlayerOne", password, f.account.PID+1), http.StatusBadRequest, "0106", "hash for another PID")

	// * The stored hash must not work as a password
	stored, err := f.store.GetAccountByPID(f.account.PID)
	if err != nil {
		t.Fatal(err)
	}

	mustBeError(t, f.post(url.Values{"grant_type": {"password"}, "user_id": {"PlayerOne"}, "password": {stored.PasswordHash}, "password_type": {"hash"}}), http.StatusBadRequest, "0106", "stored hash used as the password")

	mustBeError(t, f.post(url.Values{"grant_type": {"password"}, "password": {"x"}}), http.StatusBadRequest, "0002", "no username")
	mustBeError(t, f.post(url.Values{"grant_type": {"password"}, "user_id": {"PlayerOne"}}), http.StatusBadRequest, "0002", "no password")
	mustBeError(t, f.post(url.Values{"grant_type": {"client_credentials"}}), http.StatusBadRequest, "0004", "unsupported grant type")
	mustBeError(t, f.post(url.Values{}), http.StatusBadRequest, "0004", "no grant type")
}

func TestNEXToken(t *testing.T) {
	f := newFixture(t)
	accessToken, refreshToken := f.login()

	result := f.get("/v1/api/provider/nex_token/@me?game_server_id=1018DB00", accessToken)
	if result.status != http.StatusOK || result.root(t) != "nex_token" {
		t.Fatalf("got status %d and body %s", result.status, result.body)
	}

	if result.field(t, "host") != "203.0.113.7" || result.field(t, "port") != "6004" {
		t.Errorf("got host %q and port %q", result.field(t, "host"), result.field(t, "port"))
	}

	if result.field(t, "nex_password") != f.account.NEXPassword || result.field(t, "pid") != "1800000000" {
		t.Errorf("got NEX password %q and PID %q", result.field(t, "nex_password"), result.field(t, "pid"))
	}

	if token := result.field(t, "token"); token == "" || len(token) > 512 {
		t.Errorf("got token of %d characters, want 1 to 512", len(token))
	}

	// * Consoles send the ID in upper case, but it is a number
	if lower := f.get("/v1/api/provider/nex_token/@me?game_server_id=1018db00", accessToken); lower.root(t) != "nex_token" {
		t.Errorf("a lower case game server ID gave %s", lower.body)
	}

	mustBeError(t, f.get("/v1/api/provider/nex_token/@me?game_server_id=00003200", accessToken), http.StatusOK, "1021", "another game's server")
	mustBeError(t, f.get("/v1/api/provider/nex_token/@me", accessToken), http.StatusOK, "0118", "no game server ID")

	for what, token := range map[string]string{"no token": "", "made up token": "0123456789abcdef0123456789abcdef", "refresh token": refreshToken} {
		denied := f.get("/v1/api/provider/nex_token/@me?game_server_id=1018DB00", token)
		mustBeError(t, denied, http.StatusBadRequest, "0002", what)

		if strings.Contains(denied.body, f.account.NEXPassword) {
			t.Errorf("%s: the NEX password was given out", what)
		}
	}
}

func TestTokensExpireAndRefresh(t *testing.T) {
	f := newFixture(t)
	accessToken, refreshToken := f.login()

	const path = "/v1/api/provider/nex_token/@me?game_server_id=1018DB00"

	f.now = f.now.Add(59 * time.Minute)

	if result := f.get(path, accessToken); result.root(t) != "nex_token" {
		t.Fatalf("an access token stopped working before an hour: %s", result.body)
	}

	f.now = f.now.Add(2 * time.Minute)

	mustBeError(t, f.get(path, accessToken), http.StatusBadRequest, "0002", "access token after an hour")

	refreshed := f.post(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
	if refreshed.status != http.StatusOK || refreshed.root(t) != "OAuth20" {
		t.Fatalf("refreshing gave status %d and body %s", refreshed.status, refreshed.body)
	}

	newAccessToken := refreshed.field(t, "token")

	if newAccessToken == accessToken {
		t.Error("refreshing gave back the old access token")
	}

	if result := f.get(path, newAccessToken); result.root(t) != "nex_token" || result.field(t, "pid") != "1800000000" {
		t.Errorf("the refreshed access token gave %s", result.body)
	}

	mustBeError(t, f.post(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {accessToken}}), http.StatusBadRequest, "0106", "access token used to refresh")
	mustBeError(t, f.post(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"made up"}}), http.StatusBadRequest, "0106", "made up refresh token")
	mustBeError(t, f.post(url.Values{"grant_type": {"refresh_token"}}), http.StatusBadRequest, "0106", "no refresh token")

	f.now = f.now.Add(12 * time.Hour)

	mustBeError(t, f.post(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}), http.StatusBadRequest, "0106", "refresh token after 12 hours")
}

func TestTokensAreStoredHashed(t *testing.T) {
	f := newFixture(t)
	accessToken, refreshToken := f.login()

	for _, token := range []string{accessToken, refreshToken} {
		if _, err := f.store.GetAccountToken(token); err == nil {
			t.Errorf("token %q is stored as it is, not as a hash", token)
		}
	}
}

func TestServiceToken(t *testing.T) {
	f := newFixture(t)
	accessToken, _ := f.login()

	result := f.get("/v1/api/provider/service_token/@me?client_id=87cd32617f1985439ea608c2746e4610", accessToken)
	if result.status != http.StatusOK || result.root(t) != "service_token" || result.field(t, "token") == "" {
		t.Errorf("got status %d and body %s", result.status, result.body)
	}

	mustBeError(t, f.get("/v1/api/provider/service_token/@me", ""), http.StatusBadRequest, "0002", "no token")
}

func TestMappedIDs(t *testing.T) {
	f := newFixture(t)

	second, err := accounts.Create(f.store, "PlayerTwo", password, "")
	if err != nil {
		t.Fatal(err)
	}

	accessToken, _ := f.login()

	type mapped struct {
		In  string `xml:"in_id"`
		Out string `xml:"out_id"`
	}

	lookup := func(query string) []mapped {
		t.Helper()

		result := f.get("/v1/api/admin/mapped_ids?"+query, accessToken)
		if result.status != http.StatusOK {
			t.Fatalf("%s gave status %d: %s", query, result.status, result.body)
		}

		var document struct {
			XMLName xml.Name `xml:"mapped_ids"`
			IDs     []mapped `xml:"mapped_id"`
		}

		if err := xml.Unmarshal([]byte(result.body), &document); err != nil {
			t.Fatalf("%s gave %s: %v", query, result.body, err)
		}

		return document.IDs
	}

	// * This is the request Cemu makes
	got := lookup("input_type=user_id&output_type=pid&input=playertwo")
	if len(got) != 1 || got[0].In != "playertwo" || got[0].Out != "1800000001" {
		t.Errorf("username to PID gave %+v", got)
	}

	// * Every input gets an output, in order, which is empty when there is no match
	got = lookup("input_type=user_id&output_type=pid&input=PlayerOne,nobody,PlayerTwo")
	if len(got) != 3 || got[0].Out != "1800000000" || got[1].In != "nobody" || got[1].Out != "" || got[2].Out != "1800000001" {
		t.Errorf("several usernames gave %+v", got)
	}

	got = lookup("input_type=pid&output_type=user_id&input=1800000001,5,notanumber")
	if len(got) != 3 || got[0].Out != second.Username || got[1].Out != "" || got[2].Out != "" {
		t.Errorf("PID to username gave %+v", got)
	}

	mustBeError(t, f.get("/v1/api/admin/mapped_ids?input_type=user_id&output_type=pid", accessToken), http.StatusBadRequest, "1600", "no input")
	mustBeError(t, f.get("/v1/api/admin/mapped_ids?input_type=user_id&output_type=pid&input=PlayerOne", ""), http.StatusBadRequest, "0002", "no token")
}

func TestFailedLoginsAreLimited(t *testing.T) {
	f := newFixture(t)

	if _, err := accounts.Create(f.store, "PlayerTwo", password, ""); err != nil {
		t.Fatal(err)
	}

	for attempt := range 10 {
		mustBeError(t, f.loginAs("PlayerOne", "wrongpass", f.account.PID), http.StatusBadRequest, "0106", "failed login")

		if t.Failed() {
			t.Fatalf("stopped at attempt %d", attempt+1)
		}
	}

	// * After 10 failures, even the right password is refused
	if result := f.loginAs("PlayerOne", password, f.account.PID); result.status != http.StatusTooManyRequests {
		t.Fatalf("login after 10 failures gave status %d, want 429: %s", result.status, result.body)
	}

	// * Another account is not affected
	if result := f.loginAs("PlayerTwo", password, f.account.PID+1); result.status != http.StatusOK {
		t.Errorf("another account's login gave status %d: %s", result.status, result.body)
	}

	f.now = f.now.Add(11 * time.Minute)

	if result := f.loginAs("PlayerOne", password, f.account.PID); result.status != http.StatusOK {
		t.Errorf("login after waiting gave status %d: %s", result.status, result.body)
	}
}

func TestSuccessfulLoginsAreNotLimited(t *testing.T) {
	f := newFixture(t)

	for attempt := range 15 {
		if result := f.loginAs("PlayerOne", password, f.account.PID); result.status != http.StatusOK {
			t.Fatalf("login %d gave status %d: %s", attempt+1, result.status, result.body)
		}
	}
}

func TestUnknownPathsAndMethods(t *testing.T) {
	f := newFixture(t)
	accessToken, _ := f.login()

	if result := f.get("/v1/api/people/@me/profile", accessToken); result.status != http.StatusNotFound {
		t.Errorf("a path which is not served gave status %d", result.status)
	}

	if result := f.get("/v1/api/oauth20/access_token/generate", ""); result.status != http.StatusMethodNotAllowed {
		t.Errorf("GET on the login path gave status %d", result.status)
	}
}

func TestConfigIsChecked(t *testing.T) {
	f := newFixture(t)

	good := accountapi.Config{Accounts: f.store, GameServerHost: "203.0.113.7", GameServerPort: 6004, GameServerIDs: []string{"1018DB00"}}
	if err := good.Validate(); err != nil {
		t.Fatalf("good settings were refused: %v", err)
	}

	bad := map[string]func(config *accountapi.Config){
		"host name instead of an address": func(config *accountapi.Config) { config.GameServerHost = "smm.example.com" },
		"IPv6 address":                    func(config *accountapi.Config) { config.GameServerHost = "2001:db8::1" },
		"no host":                         func(config *accountapi.Config) { config.GameServerHost = "" },
		"port 0":                          func(config *accountapi.Config) { config.GameServerPort = 0 },
		"port too high":                   func(config *accountapi.Config) { config.GameServerPort = 70000 },
		"no game server IDs":              func(config *accountapi.Config) { config.GameServerIDs = nil },
		"short game server ID":            func(config *accountapi.Config) { config.GameServerIDs = []string{"1018DB"} },
		"game server ID which is not hex": func(config *accountapi.Config) { config.GameServerIDs = []string{"1018DBZZ"} },
		"no account storage":              func(config *accountapi.Config) { config.Accounts = nil },
	}

	for what, change := range bad {
		config := good
		change(&config)

		if err := config.Validate(); err == nil {
			t.Errorf("%s was accepted", what)
		}

		if _, err := accountapi.Handler(config); err == nil {
			t.Errorf("%s: a handler was made from bad settings", what)
		}
	}
}
