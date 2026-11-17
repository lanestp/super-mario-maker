package joinpage_test

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/joinpage"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/sqlite"
)

type setup struct {
	store  store.Store
	server *httptest.Server
	now    time.Time
}

func newSetup(t *testing.T, plugin []byte) *setup {
	t.Helper()

	if globals.Logger == nil {
		globals.Logger = plogger.NewLogger()
	}

	opened, err := sqlite.Open(filepath.Join(t.TempDir(), "smm.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { opened.Close() })

	s := &setup{store: opened, now: time.Unix(1_800_000_000, 0)}

	handler, err := joinpage.Handler(joinpage.Config{
		Accounts:         opened,
		InviteCode:       "let-me-in",
		AccountServerURL: "https://smm.example.com",
		GameServerHost:   "203.0.113.4",
		GameServerPort:   6004,
		Plugin:           plugin,
		Now:              func() time.Time { return s.now },
	})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle(joinpage.Path, handler)
	mux.Handle(joinpage.Path+"/", handler)

	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)

	return s
}

func (s *setup) post(t *testing.T, form string, values url.Values) (int, http.Header, []byte) {
	t.Helper()

	response, err := http.PostForm(s.server.URL+joinpage.Path+"/"+form, values)
	if err != nil {
		t.Fatal(err)
	}

	defer response.Body.Close()

	body, _ := io.ReadAll(response.Body)

	return response.StatusCode, response.Header, body
}

func zipNames(t *testing.T, archive []byte) []string {
	t.Helper()

	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("the answer is not a zip: %v", err)
	}

	var names []string
	for _, file := range reader.File {
		names = append(names, file.Name)
	}

	return names
}

func TestThePageShowsBothWaysToPlay(t *testing.T) {
	s := newSetup(t, []byte("plugin"))

	response, err := http.Get(s.server.URL + joinpage.Path)
	if err != nil {
		t.Fatal(err)
	}

	defer response.Body.Close()

	body, _ := io.ReadAll(response.Body)

	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `action="/join/cemu"`) || !strings.Contains(string(body), `action="/join/wiiu"`) {
		t.Fatalf("answered %d without both forms", response.StatusCode)
	}

	if !strings.Contains(response.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("the page has no content security policy")
	}
}

func TestWithoutAPluginOnlyCemuIsOffered(t *testing.T) {
	s := newSetup(t, nil)

	response, err := http.Get(s.server.URL + joinpage.Path)
	if err != nil {
		t.Fatal(err)
	}

	body, _ := io.ReadAll(response.Body)
	response.Body.Close()

	if strings.Contains(string(body), `action="/join/wiiu"`) {
		t.Error("the Wii U form is shown with no plugin to hand out")
	}

	if status, _, _ := s.post(t, "wiiu", url.Values{"invite": {"let-me-in"}, "username": {"ConsolePlayer"}, "pid": {"1712345678"}}); status != http.StatusNotFound {
		t.Errorf("joining with a Wii U answered %d, want 404", status)
	}
}

func TestJoiningWithCemuMakesTheAccountAndHandsOutItsBundle(t *testing.T) {
	s := newSetup(t, nil)

	status, header, body := s.post(t, "cemu", url.Values{"invite": {"let-me-in"}, "username": {"CemuPlayer01"}})
	if status != http.StatusOK || header.Get("Content-Type") != "application/zip" {
		t.Fatalf("answered %d %s: %s", status, header.Get("Content-Type"), body)
	}

	if !strings.Contains(header.Get("Content-Disposition"), `filename="CemuPlayer01-cemu.zip"`) {
		t.Errorf("the download is named %q", header.Get("Content-Disposition"))
	}

	names := strings.Join(zipNames(t, body), " ")
	if !strings.Contains(names, "mlc01/usr/save/system/act/80000001/account.dat") || !strings.Contains(names, "network_services.xml") {
		t.Errorf("the zip holds %s", names)
	}

	account, err := s.store.GetAccountByUsername("CemuPlayer01")
	if err != nil || account.PasswordHash == "" {
		t.Errorf("the account is %+v (%v), want one that can log in", account, err)
	}
}

func TestJoiningWithAWiiUMakesAnAccountWithThePlayersID(t *testing.T) {
	s := newSetup(t, []byte("plugin"))

	status, header, body := s.post(t, "wiiu", url.Values{"invite": {"let-me-in"}, "username": {"ConsolePlayer"}, "pid": {"1712345678"}})
	if status != http.StatusOK || header.Get("Content-Type") != "application/zip" {
		t.Fatalf("answered %d: %s", status, body)
	}

	names := strings.Join(zipNames(t, body), " ")
	if !strings.Contains(names, "wiiu/smm-private-server/1712345678.txt") || !strings.Contains(names, "smm-private-server.wps") {
		t.Errorf("the zip holds %s", names)
	}

	account, err := s.store.GetAccountByPID(1712345678)
	if err != nil || account.Username != "ConsolePlayer" || account.PasswordHash != "" {
		t.Errorf("the account is %+v (%v), want one with the given PID and no password", account, err)
	}
}

func TestNobodyJoinsWithoutTheInviteCode(t *testing.T) {
	s := newSetup(t, []byte("plugin"))

	for _, invite := range []string{"", "let-me-i", "let-me-in-please", "LET-ME-IN"} {
		status, header, _ := s.post(t, "cemu", url.Values{"invite": {invite}, "username": {"Sneaky0001"}})
		if status != http.StatusForbidden || header.Get("Content-Type") == "application/zip" {
			t.Errorf("invite %q answered %d", invite, status)
		}
	}

	if status, _, _ := s.post(t, "wiiu", url.Values{"invite": {"nope"}, "username": {"Sneaky0002"}, "pid": {"1712345678"}}); status != http.StatusForbidden {
		t.Errorf("a Wii U join with a wrong invite answered %d", status)
	}

	for _, username := range []string{"Sneaky0001", "Sneaky0002"} {
		if _, err := s.store.GetAccountByUsername(username); err == nil {
			t.Errorf("%s was made without the invite code", username)
		}
	}
}

func TestProblemsAPlayerCanFixAreExplained(t *testing.T) {
	s := newSetup(t, []byte("plugin"))

	if status, _, _ := s.post(t, "cemu", url.Values{"invite": {"let-me-in"}, "username": {"TakenName"}}); status != http.StatusOK {
		t.Fatalf("the first join answered %d", status)
	}

	// * A course owned by a PID that was on the server before
	param := datastore_types.NewDataStorePreparePostParam()
	param.DataType = 9
	param.Name = types.NewString("old course")

	dataID, nexError := s.store.InitializeObjectByPreparePostParam(types.PID(1755555555), param)
	if nexError != nil {
		t.Fatal(nexError)
	}

	if nexError := s.store.UpdateObjectUploadCompletedByDataID(types.UInt64(dataID), true); nexError != nil {
		t.Fatal(nexError)
	}

	if status, _, _ := s.post(t, "wiiu", url.Values{"invite": {"let-me-in"}, "username": {"FirstConsole"}, "pid": {"1712345678"}}); status != http.StatusOK {
		t.Fatalf("the first console join answered %d", status)
	}

	for name, try := range map[string]struct {
		form   string
		values url.Values
		status int
		says   string
	}{
		"a taken username":                 {"cemu", url.Values{"username": {"takenname"}}, http.StatusConflict, "taken"},
		"a short username":                 {"cemu", url.Values{"username": {"abc"}}, http.StatusBadRequest, "6 to 16"},
		"a taken player ID":                {"wiiu", url.Values{"username": {"SecondConsole"}, "pid": {"1712345678"}}, http.StatusConflict, "already a player"},
		"an ID that owns courses":          {"wiiu", url.Values{"username": {"ThirdConsole"}, "pid": {"1755555555"}}, http.StatusConflict, "already has courses"},
		"a player ID that is not a number": {"wiiu", url.Values{"username": {"FourthConsole"}, "pid": {"PlayerOne"}}, http.StatusBadRequest, "is a number"},
	} {
		try.values.Set("invite", "let-me-in")

		status, header, body := s.post(t, try.form, try.values)
		if status != try.status || header.Get("Content-Type") == "application/zip" || !strings.Contains(string(body), try.says) {
			t.Errorf("%s: answered %d, want %d saying %q", name, status, try.status, try.says)
		}

		// * What was typed is kept, so it does not have to be typed again
		if !strings.Contains(string(body), `value="`+try.values.Get("username")+`"`) {
			t.Errorf("%s: the username was not kept in the form", name)
		}
	}
}

func TestOneConnectionCanOnlyTrySoOften(t *testing.T) {
	s := newSetup(t, nil)

	for i := range joinpage.MaxTries {
		if status, _, _ := s.post(t, "cemu", url.Values{"invite": {"wrong"}, "username": {"Player000" + string(rune('A'+i))}}); status != http.StatusForbidden {
			t.Fatalf("try %d answered %d, want 403", i+1, status)
		}
	}

	// * Even with the right code, once the tries are used up
	if status, _, _ := s.post(t, "cemu", url.Values{"invite": {"let-me-in"}, "username": {"LatePlayer"}}); status != http.StatusTooManyRequests {
		t.Errorf("a try past the limit answered %d, want 429", status)
	}

	s.now = s.now.Add(joinpage.Window)

	if status, _, _ := s.post(t, "cemu", url.Values{"invite": {"let-me-in"}, "username": {"LatePlayer"}}); status != http.StatusOK {
		t.Errorf("a try after the window answered %d, want 200", status)
	}
}
