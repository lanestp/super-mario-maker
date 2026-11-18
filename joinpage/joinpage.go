// Package joinpage serves a page where a player with an invite code makes
// an account and downloads what they need to play: a bundle for the Cemu
// emulator, or for a Wii U that keeps using another network for everything
// but Super Mario Maker
package joinpage

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PretendoNetwork/super-mario-maker/accounts"
	"github.com/PretendoNetwork/super-mario-maker/bundle"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

// Path is where the page is served
const Path = "/join"

// Window is how long a try to join counts against an address. One
// address gets MaxTries tries, made or not, in it. Joining is done once
// per player, so this is plenty, and it keeps someone who has the
// invite code from making accounts in bulk. The rules for both are in
// rules/tries_still_counted.sir and rules/may_join.sir
const Window = time.Duration(JoinWindowMilliseconds) * time.Millisecond

//go:embed page.html
var pageFiles embed.FS

var page = template.Must(template.ParseFS(pageFiles, "page.html"))

// Config is what the page needs
type Config struct {
	Accounts store.Store

	// InviteCode is what a player has to give to join
	InviteCode string

	// AccountServerURL is how Cemu reaches the account API, such as
	// https://smm.example.com
	AccountServerURL string

	// GameServerHost and GameServerPort are where a Wii U's game is sent.
	// The host must be an IPv4 address
	GameServerHost string
	GameServerPort int

	// Plugin is the built Wii U plugin. Without one, only Cemu is offered
	Plugin []byte

	// Now is the clock. time.Now when nil
	Now func() time.Time
	// LogError is told about errors a player is not shown
	LogError func(error)
}

type joinPage struct {
	config  Config
	limiter *limiter
}

// Handler serves the page at Path, and the two forms on it
func Handler(config Config) (http.Handler, error) {
	if config.Accounts == nil || config.InviteCode == "" || config.AccountServerURL == "" {
		return nil, errors.New("joinpage: the store, an invite code and the account server's address are needed")
	}

	if config.Now == nil {
		config.Now = time.Now
	}

	if config.LogError == nil {
		config.LogError = func(error) {}
	}

	j := &joinPage{config: config, limiter: newLimiter(config.Now)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+Path, j.show)
	mux.HandleFunc("POST "+Path+"/cemu", j.joinWithCemu)
	mux.HandleFunc("POST "+Path+"/wiiu", j.joinWithWiiU)

	return securityHeaders(mux), nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")

		next.ServeHTTP(w, r)
	})
}

// * What the page is drawn with
type view struct {
	WiiU     bool
	Error    string
	ErrorFor string
	Username string
	PID      string
	Invite   string
}

func (j *joinPage) show(w http.ResponseWriter, r *http.Request) {
	j.render(w, http.StatusOK, view{WiiU: len(j.config.Plugin) > 0})
}

func (j *joinPage) render(w http.ResponseWriter, status int, v view) {
	v.WiiU = len(j.config.Plugin) > 0

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)

	if err := page.Execute(w, v); err != nil {
		j.config.LogError(fmt.Errorf("drawing the join page: %w", err))
	}
}

// * Reads a form, and checks the address may try and the invite code.
// * Returns false when it has already answered
func (j *joinPage) admit(w http.ResponseWriter, r *http.Request, form string) (view, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	v := view{ErrorFor: form}

	if err := r.ParseForm(); err != nil {
		v.Error = "That form could not be read."
		j.render(w, http.StatusBadRequest, v)

		return v, false
	}

	v.Username = strings.TrimSpace(r.PostForm.Get("username"))
	v.PID = strings.TrimSpace(r.PostForm.Get("pid"))
	v.Invite = r.PostForm.Get("invite")

	if !j.limiter.allow(clientAddress(r)) {
		v.Error = "Too many tries from your connection. Try again in an hour."
		j.render(w, http.StatusTooManyRequests, v)

		return v, false
	}

	if !j.inviteMatches(v.Invite) {
		v.Error = "That invite code is not right."
		j.render(w, http.StatusForbidden, v)

		return v, false
	}

	return v, true
}

func (j *joinPage) inviteMatches(given string) bool {
	// * Compared as hashes, so that how long the comparison takes says
	// * nothing about the code, not even its length
	want := sha256.Sum256([]byte(j.config.InviteCode))
	got := sha256.Sum256([]byte(strings.TrimSpace(given)))

	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

func (j *joinPage) joinWithCemu(w http.ResponseWriter, r *http.Request) {
	v, admitted := j.admit(w, r, "cemu")
	if !admitted {
		return
	}

	// * The player never types this. It only lives on as a hash, in the
	// * account file and in the database
	password, err := store.GenerateNEXPassword()
	if err != nil {
		j.fail(w, v, err)
		return
	}

	account, err := accounts.Create(j.config.Accounts, v.Username, password, v.Username)
	if err != nil {
		j.refuse(w, v, err)
		return
	}

	archive, err := bundle.Cemu(account, password, j.config.AccountServerURL)
	if err != nil {
		j.fail(w, v, err)
		return
	}

	sendZip(w, account.Username+"-cemu.zip", archive)
}

func (j *joinPage) joinWithWiiU(w http.ResponseWriter, r *http.Request) {
	if len(j.config.Plugin) == 0 {
		http.NotFound(w, r)
		return
	}

	v, admitted := j.admit(w, r, "wiiu")
	if !admitted {
		return
	}

	pid, err := strconv.ParseUint(v.PID, 10, 32)
	if err != nil || pid == 0 {
		v.Error = "A player ID is a number, such as 1712345678."
		j.render(w, http.StatusBadRequest, v)

		return
	}

	account, err := accounts.CreateForConsole(j.config.Accounts, j.config.Accounts, v.Username, uint32(pid))
	if err != nil {
		j.refuse(w, v, err)
		return
	}

	archive, err := bundle.WiiU(account, j.config.GameServerHost, j.config.GameServerPort, j.config.Plugin)
	if err != nil {
		j.fail(w, v, err)
		return
	}

	sendZip(w, account.Username+"-wiiu.zip", archive)
}

// * Answers a request that could not make an account, saying why when
// * the player can do something about it
func (j *joinPage) refuse(w http.ResponseWriter, v view, err error) {
	switch {
	case errors.Is(err, accounts.ErrInvalidUsername):
		v.Error = "A username is 6 to 16 letters, numbers, dots, dashes and underscores."
		j.render(w, http.StatusBadRequest, v)
	case errors.Is(err, store.ErrUsernameTaken):
		v.Error = "That username is taken. Pick another."
		j.render(w, http.StatusConflict, v)
	case errors.Is(err, store.ErrPIDTaken):
		v.Error = "There is already a player with that player ID here. If it is you, ask whoever runs the server."
		j.render(w, http.StatusConflict, v)
	case errors.Is(err, accounts.ErrPIDHasCourses):
		v.Error = "That player ID already has courses on this server, so it can not be used for a new player. Ask whoever runs the server."
		j.render(w, http.StatusConflict, v)
	default:
		j.fail(w, v, err)
	}
}

func (j *joinPage) fail(w http.ResponseWriter, v view, err error) {
	j.config.LogError(fmt.Errorf("joining as %q: %w", v.Username, err))

	v.Error = "Something went wrong on the server. Try again later."
	j.render(w, http.StatusInternalServerError, v)
}

func sendZip(w http.ResponseWriter, name string, archive []byte) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
	w.WriteHeader(http.StatusOK)
	w.Write(archive)
}

// * The server is reached directly, with nothing in front of it, so the
// * connection's own address is the player's
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

// * Keeps when each address tried to join, in milliseconds since 1970
type limiter struct {
	mutex sync.Mutex
	now   func() time.Time
	tries map[string][]int64
}

func newLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, tries: map[string][]int64{}}
}

func (l *limiter) allow(address string) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	now := l.now().UnixMilli()

	l.forgetOldTries(now)

	if !MayJoin(int64(len(l.tries[address]))) {
		return false
	}

	l.tries[address] = append(l.tries[address], now)

	return true
}

// * Forgets the tries that no longer count, for every address, so the
// * map does not grow for ever
func (l *limiter) forgetOldTries(now int64) {
	for address, times := range l.tries {
		if counted := TriesStillCounted(times, now); len(counted) > 0 {
			l.tries[address] = counted
		} else {
			delete(l.tries, address)
		}
	}
}
