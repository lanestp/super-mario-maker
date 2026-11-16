package store

import (
	"crypto/rand"
	"errors"
	"time"
)

// ErrAccountNotFound is returned when no account matches
var ErrAccountNotFound = errors.New("account not found")

// ErrUsernameTaken is returned when creating an account with a username
// another account already has. Usernames are compared without regard to case
var ErrUsernameTaken = errors.New("username is already taken")

// FirstPID is the PID given to the first account created. PIDs below
// this are left free for the special accounts NEX itself uses
const FirstPID = 1800000000

// Account is a player's account, for servers which keep their own
// accounts instead of using an account server
type Account struct {
	PID      uint32
	Username string

	// PasswordHash is used to log in over HTTP. It is empty until
	// set, and an account with no password hash can not log in
	PasswordHash string

	// NEXPassword is the secret the game server and console share
	NEXPassword string

	MiiName   string
	MiiData   []byte
	CreatedAt time.Time
}

// NewAccount is the information needed to create an account
type NewAccount struct {
	Username string
	MiiName  string
	MiiData  []byte
}

// AccountStore is the storage for accounts
type AccountStore interface {
	// CreateAccount creates an account with a new PID and NEX password
	CreateAccount(account NewAccount) (Account, error)
	SetAccountPasswordHash(pid uint32, passwordHash string) error
	GetAccountByPID(pid uint32) (Account, error)
	// GetAccountByUsername finds an account without regard to the case of the username
	GetAccountByUsername(username string) (Account, error)
}

const nexPasswordCharacters = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// GenerateNEXPassword makes a new random NEX password
func GenerateNEXPassword() (string, error) {
	const length = 16

	// * 248 is the largest multiple of the character count which fits
	// * in a byte. Bytes above it are thrown away so that every
	// * character is equally likely
	const limit = 248

	password := make([]byte, 0, length)
	random := make([]byte, 1)

	for len(password) < length {
		if _, err := rand.Read(random); err != nil {
			return "", err
		}

		if random[0] >= limit {
			continue
		}

		password = append(password, nexPasswordCharacters[int(random[0])%len(nexPasswordCharacters)])
	}

	return string(password), nil
}
