// Package accounts manages player accounts for servers which keep their
// own, instead of using an account server
package accounts

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"regexp"

	"github.com/PretendoNetwork/super-mario-maker/store"
	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidUsername is returned when a username is not 6 to 16
// letters, numbers, dots, dashes and underscores
var ErrInvalidUsername = errors.New("username must be 6 to 16 characters, using only letters, numbers, dots, dashes and underscores")

// ErrInvalidPassword is returned when a password is too short or too long
var ErrInvalidPassword = errors.New("password must be 6 to 16 characters")

// ErrWrongPassword is returned when a password does not match the account
var ErrWrongPassword = errors.New("wrong password")

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{6,16}$`)

// ValidUsername returns ErrInvalidUsername for a username that can not be used
func ValidUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return ErrInvalidUsername
	}

	return nil
}

// NintendoPasswordHash hashes a password the way a console does before
// sending it. The console never sends the password itself
func NintendoPasswordHash(pid uint32, password string) string {
	data := binary.LittleEndian.AppendUint32(nil, pid)
	data = append(data, 0x02, 0x65, 0x43, 0x46)
	data = append(data, password...)

	hash := sha256.Sum256(data)

	return hex.EncodeToString(hash[:])
}

// Create makes a new account which can log in with the given password
func Create(accountStore store.AccountStore, username, password, miiName string) (store.Account, error) {
	if err := ValidUsername(username); err != nil {
		return store.Account{}, err
	}

	if len(password) < 6 || len(password) > 16 {
		return store.Account{}, ErrInvalidPassword
	}

	if miiName == "" {
		miiName = username
	}

	account, err := accountStore.CreateAccount(store.NewAccount{Username: username, MiiName: miiName})
	if err != nil {
		return store.Account{}, err
	}

	// * The hash a console sends includes the PID, so
	// * it can only be made once the account exists
	if err := SetPassword(accountStore, account.PID, password); err != nil {
		return store.Account{}, err
	}

	return accountStore.GetAccountByPID(account.PID)
}

// SetPassword changes the password an account logs in with
func SetPassword(accountStore store.AccountStore, pid uint32, password string) error {
	if len(password) < 6 || len(password) > 16 {
		return ErrInvalidPassword
	}

	// * What is stored is a hash of the hash a console
	// * sends, so that a copy of the database is not
	// * enough to log in as someone
	stored, err := bcrypt.GenerateFromPassword([]byte(NintendoPasswordHash(pid, password)), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	return accountStore.SetAccountPasswordHash(pid, string(stored))
}

// CheckPasswordHash reports whether the hash a console sent is the right one for the account
func CheckPasswordHash(account store.Account, nintendoPasswordHash string) error {
	if account.PasswordHash == "" {
		return ErrWrongPassword
	}

	if bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(nintendoPasswordHash)) != nil {
		return ErrWrongPassword
	}

	return nil
}

// CheckPassword reports whether a password is the right one for the account
func CheckPassword(account store.Account, password string) error {
	return CheckPasswordHash(account, NintendoPasswordHash(account.PID, password))
}
