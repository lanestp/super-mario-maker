// Command smm-account manages the accounts of a server which keeps its
// own (PN_SMM_ACCOUNTS=local). It uses the same database settings as the server
//
//	smm-account create <username>
//	smm-account password <username>
//	smm-account show <username>
//
// The password is read from the SMM_ACCOUNT_PASSWORD environment
// variable, or asked for if that is not set
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/accounts"
	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/store"
	"github.com/PretendoNetwork/super-mario-maker/store/open"
	"github.com/joho/godotenv"
	"golang.org/x/term"
)

const usage = `usage:
  smm-account create <username>     create an account
  smm-account password <username>   change the password of an account
  smm-account show <username>       show the PID of an account`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 2 {
		return errors.New(usage)
	}

	command, username := args[0], args[1]

	if command != "create" && command != "password" && command != "show" {
		return errors.New(usage)
	}

	// * A missing .env file is fine, the settings may come from the environment
	_ = godotenv.Load()

	globals.Logger = plogger.NewLogger()

	opened, err := open.FromEnv()
	if err != nil {
		return err
	}

	defer opened.Close()

	switch command {
	case "create":
		return create(opened, username)
	case "password":
		return changePassword(opened, username)
	default:
		return show(opened, username)
	}
}

func create(accountStore store.AccountStore, username string) error {
	password, err := readPassword()
	if err != nil {
		return err
	}

	account, err := accounts.Create(accountStore, username, password, "")
	if err != nil {
		return err
	}

	fmt.Printf("Created account %s with PID %d\n", account.Username, account.PID)

	return nil
}

func changePassword(accountStore store.AccountStore, username string) error {
	account, err := accountStore.GetAccountByUsername(username)
	if err != nil {
		return err
	}

	password, err := readPassword()
	if err != nil {
		return err
	}

	if err := accounts.SetPassword(accountStore, account.PID, password); err != nil {
		return err
	}

	fmt.Printf("Changed the password of account %s\n", account.Username)

	return nil
}

func show(accountStore store.AccountStore, username string) error {
	account, err := accountStore.GetAccountByUsername(username)
	if err != nil {
		return err
	}

	fmt.Printf("%s has PID %d and was created %s\n", account.Username, account.PID, account.CreatedAt.Format("2006-01-02"))

	return nil
}

func readPassword() (string, error) {
	if password := os.Getenv("SMM_ACCOUNT_PASSWORD"); password != "" {
		return password, nil
	}

	stdin := int(os.Stdin.Fd())

	if term.IsTerminal(stdin) {
		fmt.Fprint(os.Stderr, "Password: ")

		password, err := term.ReadPassword(stdin)
		fmt.Fprintln(os.Stderr)

		return string(password), err
	}

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no password given")
	}

	return strings.TrimRight(line, "\r\n"), nil
}
