package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/not-emily/bardbookserver/internal/auth"
	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/model"
)

func cmdUser(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: bard user <add|passwd|pin|list> [flags]")
	}
	switch args[0] {
	case "add":
		return cmdUserAdd(args[1:])
	case "passwd":
		return cmdUserPasswd(args[1:])
	case "pin":
		return cmdUserPIN(args[1:])
	case "list":
		return cmdUserList(args[1:])
	default:
		return fmt.Errorf("unknown user command %q", args[0])
	}
}

// openStore loads config and opens the database for a one-shot CLI command.
func openStore(configPath string) (*db.DB, *auth.Store, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, err
	}
	database, err := db.Open(context.Background(), cfg.DatabasePath())
	if err != nil {
		return nil, nil, err
	}
	return database, auth.NewStore(database.DB), nil
}

func cmdUserAdd(args []string) error {
	fs := flag.NewFlagSet("user add", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to bard.yml")
	username := fs.String("username", "", "login name (required)")
	display := fs.String("display-name", "", "name shown in clients (defaults to username)")
	admin := fs.Bool("admin", false, "grant admin rights")
	upload := fs.Bool("upload", false, "allow uploading books")
	managed := fs.Bool("managed", false, "create a passwordless profile switched into from an owner's device")
	owner := fs.String("owner", "", "owning account username (required with --managed)")
	birth := fs.String("birth-date", "", "YYYY-MM-DD; required for a restricted account")
	restricted := fs.Bool("restricted", false, "apply age restrictions to this account")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *username == "" {
		return errors.New("--username is required")
	}

	database, store, err := openStore(*configPath)
	if err != nil {
		return err
	}
	defer database.Close()

	ctx := context.Background()
	in := auth.NewUser{
		Username:    *username,
		DisplayName: *display,
		IsAdmin:     *admin,
		CanUpload:   *upload,
		Managed:     *managed,
		Restricted:  *restricted,
	}

	if *restricted && *birth == "" {
		return errors.New("--birth-date is required for a restricted account")
	}
	if *birth != "" {
		t, err := time.Parse(model.DateLayout, *birth)
		if err != nil {
			return fmt.Errorf("--birth-date must be YYYY-MM-DD: %w", err)
		}
		in.BirthDate = &t
	}

	if *managed {
		if *owner == "" {
			return errors.New("--owner is required with --managed")
		}
		ownerUser, err := store.UserByUsername(ctx, *owner)
		if errors.Is(err, auth.ErrNotFound) {
			return fmt.Errorf("no such owner account %q", *owner)
		}
		if err != nil {
			return err
		}
		if ownerUser.Managed {
			return errors.New("a managed profile cannot own another")
		}
		in.OwnerID = &ownerUser.ID
	} else {
		password, err := readSecret("Password: ", true)
		if err != nil {
			return err
		}
		if len(password) < 8 {
			return errors.New("password must be at least 8 characters")
		}
		in.Password = password
	}

	// The first account is made an admin regardless, since an install with no
	// admin cannot be administered.
	count, err := store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		if *managed {
			return errors.New("the first account must be a credentialed admin, not a managed profile")
		}
		in.IsAdmin = true
		in.CanUpload = true
	}

	user, err := store.CreateUser(ctx, in)
	if errors.Is(err, auth.ErrUsernameTaken) {
		return fmt.Errorf("username %q is already taken", *username)
	}
	if err != nil {
		return err
	}

	fmt.Printf("created %s (id %d)%s%s\n", user.Username, user.ID,
		label(user.IsAdmin, " admin"), label(user.Managed, " managed"))
	return nil
}

func cmdUserPasswd(args []string) error {
	fs := flag.NewFlagSet("user passwd", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to bard.yml")
	username := fs.String("username", "", "login name (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *username == "" {
		return errors.New("--username is required")
	}

	database, store, err := openStore(*configPath)
	if err != nil {
		return err
	}
	defer database.Close()

	ctx := context.Background()
	user, err := store.UserByUsername(ctx, *username)
	if errors.Is(err, auth.ErrNotFound) {
		return fmt.Errorf("no such account %q", *username)
	}
	if err != nil {
		return err
	}
	if user.Managed {
		return errors.New("a managed profile has no password; set a PIN instead")
	}

	password, err := readSecret("New password: ", true)
	if err != nil {
		return err
	}
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if err := store.SetPassword(ctx, user.ID, password); err != nil {
		return err
	}

	fmt.Printf("password updated for %s\n", user.Username)
	return nil
}

func cmdUserPIN(args []string) error {
	fs := flag.NewFlagSet("user pin", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to bard.yml")
	username := fs.String("username", "", "login name (required)")
	clear := fs.Bool("clear", false, "remove the PIN")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *username == "" {
		return errors.New("--username is required")
	}

	database, store, err := openStore(*configPath)
	if err != nil {
		return err
	}
	defer database.Close()

	ctx := context.Background()
	user, err := store.UserByUsername(ctx, *username)
	if errors.Is(err, auth.ErrNotFound) {
		return fmt.Errorf("no such account %q", *username)
	}
	if err != nil {
		return err
	}

	pin := ""
	if !*clear {
		pin, err = readSecret("PIN: ", true)
		if err != nil {
			return err
		}
		if len(pin) < 4 {
			return errors.New("PIN must be at least 4 characters")
		}
	}
	if err := store.SetPIN(ctx, user.ID, pin); err != nil {
		return err
	}

	if *clear {
		fmt.Printf("PIN cleared for %s\n", user.Username)
	} else {
		fmt.Printf("PIN set for %s\n", user.Username)
	}
	return nil
}

func cmdUserList(args []string) error {
	fs := flag.NewFlagSet("user list", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to bard.yml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	database, store, err := openStore(*configPath)
	if err != nil {
		return err
	}
	defer database.Close()

	ctx := context.Background()
	count, err := store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		fmt.Println("no accounts yet; create one with: bard user add --username NAME")
		return nil
	}

	rows, err := database.QueryContext(ctx,
		`SELECT id, username, is_admin, managed, restricted, birth_date FROM users ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Printf("%-4s %-20s %-7s %-8s %s\n", "ID", "USERNAME", "ADMIN", "MANAGED", "RESTRICTED")
	for rows.Next() {
		var (
			id                               int64
			username                         string
			isAdmin, isManaged, isRestricted bool
			birthDate                        *string
		)
		if err := rows.Scan(&id, &username, &isAdmin, &isManaged, &isRestricted, &birthDate); err != nil {
			return err
		}
		restricted := "no"
		if isRestricted && birthDate != nil {
			restricted = "yes (born " + *birthDate + ")"
		}
		fmt.Printf("%-4d %-20s %-7v %-8v %s\n", id, username, isAdmin, isManaged, restricted)
	}
	return rows.Err()
}

func label(on bool, text string) string {
	if on {
		return text
	}
	return ""
}

// readSecret reads without echo from a terminal, and falls back to a plain
// line when stdin is a pipe so the commands stay scriptable.
func readSecret(prompt string, confirm bool) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("read secret from stdin: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	fmt.Fprint(os.Stderr, prompt)
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read secret: %w", err)
	}

	if confirm {
		fmt.Fprint(os.Stderr, "Confirm: ")
		second, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read confirmation: %w", err)
		}
		if string(first) != string(second) {
			return "", errors.New("entries did not match")
		}
	}
	return string(first), nil
}
