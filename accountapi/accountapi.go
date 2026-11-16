// Package accountapi is a small replacement for the parts of the
// Nintendo Network account server a console needs to reach this game
// server: logging in, and being told where the game server is.
//
// It is only for servers which keep their own accounts
// (PN_SMM_ACCOUNTS=local). Accounts are made with the smm-account
// command, not through this API
package accountapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PretendoNetwork/super-mario-maker/accounts"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

const (
	accessTokenLifetime  = time.Hour
	refreshTokenLifetime = 12 * time.Hour
)

// Config is the settings of the account API
type Config struct {
	Accounts store.AccountStore

	// GameServerHost is the IPv4 address consoles connect to the game server
	// on. Consoles can not use a host name here, or anything over 15 characters
	GameServerHost string

	// GameServerPort is the port of the authentication server
	GameServerPort int

	// GameServerIDs are the game server IDs this server answers for, as 8
	// hexadecimal digits. A console asking for any other is told it was not found
	GameServerIDs []string

	// Now returns the current time. It is time.Now when not set
	Now func() time.Time

	// LogError is called with errors which are not the fault of the request
	LogError func(err error)
}

// Validate reports whether the settings can be used
func (config Config) Validate() error {
	if config.Accounts == nil {
		return errors.New("no account storage given")
	}

	ip := net.ParseIP(config.GameServerHost)
	if ip == nil || ip.To4() == nil {
		return errors.New("the game server host must be an IPv4 address, as consoles can not use a host name, got " + strconv.Quote(config.GameServerHost))
	}

	if config.GameServerPort < 1 || config.GameServerPort > 65535 {
		return errors.New("the game server port must be 1-65535, got " + strconv.Itoa(config.GameServerPort))
	}

	if len(config.GameServerIDs) == 0 {
		return errors.New("no game server IDs given")
	}

	for _, id := range config.GameServerIDs {
		if _, err := strconv.ParseUint(id, 16, 32); err != nil || len(id) != 8 {
			return errors.New("game server IDs must be 8 hexadecimal digits, got " + strconv.Quote(id))
		}
	}

	return nil
}

type api struct {
	config  Config
	limiter *loginLimiter
}

// Handler returns the HTTP handler of the account API
func Handler(config Config) (http.Handler, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	if config.Now == nil {
		config.Now = time.Now
	}

	if config.LogError == nil {
		config.LogError = func(error) {}
	}

	a := &api{config: config, limiter: newLoginLimiter(config.Now)}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/api/oauth20/access_token/generate", a.generateAccessToken)
	mux.HandleFunc("GET /v1/api/provider/nex_token/@me", a.nexToken)
	mux.HandleFunc("GET /v1/api/provider/service_token/@me", a.serviceToken)
	mux.HandleFunc("GET /v1/api/admin/mapped_ids", a.mappedIDs)

	return mux, nil
}

// * The XML documents a console expects

type errorsDocument struct {
	XMLName xml.Name      `xml:"errors"`
	Errors  []errorDetail `xml:"error"`
}

type errorDetail struct {
	Cause   string `xml:"cause,omitempty"`
	Code    string `xml:"code"`
	Message string `xml:"message"`
}

type oauthDocument struct {
	XMLName     xml.Name `xml:"OAuth20"`
	AccessToken struct {
		Token        string `xml:"token"`
		RefreshToken string `xml:"refresh_token"`
		ExpiresIn    int    `xml:"expires_in"`
	} `xml:"access_token"`
}

type nexTokenDocument struct {
	XMLName     xml.Name `xml:"nex_token"`
	Host        string   `xml:"host"`
	NEXPassword string   `xml:"nex_password"`
	PID         uint32   `xml:"pid"`
	Port        int      `xml:"port"`
	Token       string   `xml:"token"`
}

type serviceTokenDocument struct {
	XMLName xml.Name `xml:"service_token"`
	Token   string   `xml:"token"`
}

type mappedIDsDocument struct {
	XMLName   xml.Name   `xml:"mapped_ids"`
	MappedIDs []mappedID `xml:"mapped_id"`
}

type mappedID struct {
	InID  string `xml:"in_id"`
	OutID string `xml:"out_id"`
}

func writeXML(w http.ResponseWriter, status int, document any) {
	body, err := xml.Marshal(document)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/xml;charset=UTF-8")
	w.WriteHeader(status)
	w.Write([]byte(xml.Header))
	w.Write(body)
}

func writeError(w http.ResponseWriter, status int, cause, code, message string) {
	writeXML(w, status, errorsDocument{Errors: []errorDetail{{Cause: cause, Code: code, Message: message}}})
}

func (a *api) internalError(w http.ResponseWriter, err error) {
	a.config.LogError(err)
	writeError(w, http.StatusInternalServerError, "", "2001", "Internal server error")
}

func randomToken(bytes int) (string, []byte, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}

	return hex.EncodeToString(raw), raw, nil
}

// * Tokens are stored as a hash, so that a copy of
// * the database does not hold anything usable
func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))

	return hex.EncodeToString(hash[:])
}

func (a *api) generateAccessToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)

	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}

	var account store.Account

	switch r.PostForm.Get("grant_type") {
	case "password":
		username := strings.TrimSpace(r.PostForm.Get("user_id"))
		if username == "" {
			writeError(w, http.StatusBadRequest, "user_id", "0002", "user_id format is invalid")
			return
		}

		password := r.PostForm.Get("password")
		if strings.TrimSpace(password) == "" {
			writeError(w, http.StatusBadRequest, "password", "0002", "password format is invalid")
			return
		}

		limiterKey := clientIP(r) + " " + strings.ToLower(username)

		if !a.limiter.allowed(limiterKey) {
			writeError(w, http.StatusTooManyRequests, "", "0106", "Too many failed logins, try again later")
			return
		}

		found, err := a.config.Accounts.GetAccountByUsername(username)
		if err != nil && !errors.Is(err, store.ErrAccountNotFound) {
			a.internalError(w, err)
			return
		}

		// * A console sends a hash of the password, and says so
		var passwordError error
		if r.PostForm.Get("password_type") == "hash" {
			passwordError = accounts.CheckPasswordHash(found, password)
		} else {
			passwordError = accounts.CheckPassword(found, password)
		}

		// * The same answer is given for an unknown account and a
		// * wrong password, so that this can not be used to find accounts
		if err != nil || passwordError != nil {
			a.limiter.failed(limiterKey)
			writeError(w, http.StatusBadRequest, "", "0106", "Invalid account ID or password")
			return
		}

		account = found
	case "refresh_token":
		refreshToken := strings.TrimSpace(r.PostForm.Get("refresh_token"))

		pid, ok := a.tokenOwner(w, refreshToken, store.TokenKindRefresh)
		if !ok {
			if pid == 0 {
				writeError(w, http.StatusBadRequest, "refresh_token", "0106", "Invalid Refresh Token")
			}

			return
		}

		found, err := a.config.Accounts.GetAccountByPID(pid)
		if err != nil {
			writeError(w, http.StatusBadRequest, "refresh_token", "0106", "Invalid Refresh Token")
			return
		}

		account = found
	default:
		writeError(w, http.StatusBadRequest, "grant_type", "0004", "Invalid Grant Type")
		return
	}

	accessToken, _, err := randomToken(16)
	if err != nil {
		a.internalError(w, err)
		return
	}

	refreshToken, _, err := randomToken(20)
	if err != nil {
		a.internalError(w, err)
		return
	}

	now := a.config.Now()

	// * Old tokens are cleared out as new ones are made
	if err := a.config.Accounts.DeleteAccountTokensExpiredBefore(now); err != nil {
		a.internalError(w, err)
		return
	}

	err = a.config.Accounts.CreateAccountToken(hashToken(accessToken), store.AccountToken{PID: account.PID, Kind: store.TokenKindAccess, ExpiresAt: now.Add(accessTokenLifetime)})
	if err != nil {
		a.internalError(w, err)
		return
	}

	err = a.config.Accounts.CreateAccountToken(hashToken(refreshToken), store.AccountToken{PID: account.PID, Kind: store.TokenKindRefresh, ExpiresAt: now.Add(refreshTokenLifetime)})
	if err != nil {
		a.internalError(w, err)
		return
	}

	var document oauthDocument
	document.AccessToken.Token = accessToken
	document.AccessToken.RefreshToken = refreshToken
	document.AccessToken.ExpiresIn = int(accessTokenLifetime.Seconds())

	writeXML(w, http.StatusOK, document)
}

// * Returns the PID a token of the given kind belongs to. When it returns
// * false with a PID of 0 the token is not valid, and nothing has been
// * written. When it returns false with another PID, an error has been written
func (a *api) tokenOwner(w http.ResponseWriter, token, kind string) (uint32, bool) {
	if token == "" {
		return 0, false
	}

	found, err := a.config.Accounts.GetAccountToken(hashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrTokenNotFound) {
			return 0, false
		}

		a.internalError(w, err)

		return 1, false
	}

	if found.Kind != kind || !a.config.Now().Before(found.ExpiresAt) {
		return 0, false
	}

	return found.PID, true
}

// * Returns the account the request's access token belongs to, or writes an error
func (a *api) authenticate(w http.ResponseWriter, r *http.Request) (store.Account, bool) {
	token, isBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")

	pid, ok := uint32(0), false
	if isBearer {
		pid, ok = a.tokenOwner(w, strings.TrimSpace(token), store.TokenKindAccess)
	}

	if !ok {
		if pid == 0 {
			writeError(w, http.StatusBadRequest, "access_token", "0002", "Invalid access token")
		}

		return store.Account{}, false
	}

	account, err := a.config.Accounts.GetAccountByPID(pid)
	if err != nil {
		if errors.Is(err, store.ErrAccountNotFound) {
			writeError(w, http.StatusBadRequest, "access_token", "0002", "Invalid access token")
		} else {
			a.internalError(w, err)
		}

		return store.Account{}, false
	}

	return account, true
}

func (a *api) nexToken(w http.ResponseWriter, r *http.Request) {
	account, ok := a.authenticate(w, r)
	if !ok {
		return
	}

	gameServerID := strings.TrimSpace(r.URL.Query().Get("game_server_id"))
	if gameServerID == "" {
		writeError(w, http.StatusOK, "", "0118", "Unique ID and Game Server ID are not linked")
		return
	}

	known := false
	for _, id := range a.config.GameServerIDs {
		if strings.EqualFold(id, gameServerID) {
			known = true
		}
	}

	if !known {
		writeError(w, http.StatusOK, "", "1021", "The requested game server was not found")
		return
	}

	// * The game server does not check this token, it checks that
	// * the console knows the NEX password. A console still expects one
	_, raw, err := randomToken(36)
	if err != nil {
		a.internalError(w, err)
		return
	}

	writeXML(w, http.StatusOK, nexTokenDocument{
		Host:        a.config.GameServerHost,
		NEXPassword: account.NEXPassword,
		PID:         account.PID,
		Port:        a.config.GameServerPort,
		Token:       base64.StdEncoding.EncodeToString(raw),
	})
}

func (a *api) serviceToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticate(w, r); !ok {
		return
	}

	// * Nothing on this server uses service tokens, but games ask for one
	_, raw, err := randomToken(36)
	if err != nil {
		a.internalError(w, err)
		return
	}

	writeXML(w, http.StatusOK, serviceTokenDocument{Token: base64.StdEncoding.EncodeToString(raw)})
}

func (a *api) mappedIDs(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticate(w, r); !ok {
		return
	}

	query := r.URL.Query()
	inputType, outputType, input := query.Get("input_type"), query.Get("output_type"), query.Get("input")

	if inputType == "" || outputType == "" || input == "" {
		writeError(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}

	const maxInputs = 100

	document := mappedIDsDocument{}

	// * Every input always gets an output, which is empty when there is no match
	for _, in := range strings.Split(input, ",") {
		if in == "" {
			continue
		}

		if len(document.MappedIDs) == maxInputs {
			break
		}

		mapped := mappedID{InID: in}

		var account store.Account
		var err error

		switch inputType {
		case "user_id":
			account, err = a.config.Accounts.GetAccountByUsername(in)
		case "pid":
			pid, parseError := strconv.ParseUint(in, 10, 32)
			if parseError != nil {
				err = store.ErrAccountNotFound
			} else {
				account, err = a.config.Accounts.GetAccountByPID(uint32(pid))
			}
		default:
			err = store.ErrAccountNotFound
		}

		if err != nil && !errors.Is(err, store.ErrAccountNotFound) {
			a.internalError(w, err)
			return
		}

		if err == nil {
			switch outputType {
			case "user_id":
				mapped.OutID = account.Username
			case "pid":
				mapped.OutID = strconv.FormatUint(uint64(account.PID), 10)
			}
		}

		document.MappedIDs = append(document.MappedIDs, mapped)
	}

	writeXML(w, http.StatusOK, document)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}
