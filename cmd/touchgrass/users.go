package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/oralecarlangelo/touchgrass/internal/config"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const usersUsageText = `Usage: touchgrass users reset-password [--username admin]

Subcommands:
  reset-password [--username NAME]
        give one account a generated one-time password and print it once.
        The account can then only change its password until it has. Use it
        when a password is lost; run it on the host, as the service user,
        with the same TOUCHGRASS_DB the server uses.
`

// initialPasswordFile is where a generated one-time admin password
// lives: beside the database, so it shares the data directory's
// ownership, backups and volume.
func initialPasswordFile(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), service.InitialPasswordFileName)
}

// ensureUsers wires managed accounts and guarantees a usable admin.
func ensureUsers(
	ctx context.Context,
	db *store.DB,
	audit *service.Audit,
	cfg config.Config,
	logger *slog.Logger,
) (*service.Users, error) {
	users := service.NewUsers(store.NewUserStore(db), audit).
		WithInitialPasswordFile(initialPasswordFile(cfg.DB))

	bootstrap, err := users.EnsureAdmin(ctx, cfg.AdminPassword)
	if err != nil {
		return nil, fmt.Errorf("ensuring admin account: %w", err)
	}

	logAdminBootstrap(logger, bootstrap)

	return users, nil
}

// logAdminBootstrap tells the operator how the admin can log in. It
// names the file holding a generated password, never the password.
func logAdminBootstrap(logger *slog.Logger, bootstrap service.AdminBootstrap) {
	switch {
	case bootstrap.Seeded && bootstrap.Generated:
		logger.Warn("no enabled admin found; generated a one-time admin password — log in as admin and replace it",
			"password_file", bootstrap.PasswordFile)
	case bootstrap.Seeded:
		logger.Warn("seeded the admin password from TOUCHGRASS_ADMIN_PASSWORD")
	case bootstrap.Pending && bootstrap.PasswordFile != "":
		logger.Warn("the admin has not replaced its one-time password yet",
			"password_file", bootstrap.PasswordFile)
	case bootstrap.Pending:
		logger.Warn("the admin has not replaced its one-time password yet; " +
			"if it is lost, run `touchgrass users reset-password`")
	}
}

// runUsers dispatches the users subcommands.
func runUsers(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		if _, err := io.WriteString(os.Stdout, usersUsageText); err != nil {
			return fmt.Errorf("writing usage: %w", err)
		}

		return nil
	}

	switch args[0] {
	case "reset-password":
		return runUsersResetPassword(args[1:], os.Stdout)
	default:
		return fmt.Errorf("unknown users subcommand %q: want reset-password", args[0])
	}
}

// runUsersResetPassword gives one account a one-time password and
// prints it once to out. It works against the live database, so the
// running server sees the change on the account's next request.
func runUsersResetPassword(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("users reset-password", flag.ContinueOnError)
	username := fs.String("username", "admin", "account to reset")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return fmt.Errorf("parsing users reset-password flags: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}

	password, err := resetPassword(context.Background(), db, cfg, *username)

	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("closing database: %w", closeErr))
	}

	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out,
		"One-time password for %q (shown once; it must be replaced at the next login):\n%s\n", *username, password)
	if err != nil {
		return fmt.Errorf("printing one-time password: %w", err)
	}

	return nil
}

// resetPassword refuses a database this binary has not migrated, then
// resets the account through the users service so the change is
// audited like any other.
func resetPassword(ctx context.Context, db *store.DB, cfg config.Config, username string) (string, error) {
	_, pending, err := db.MigrationStatus(ctx)
	if err != nil {
		return "", err
	}

	if len(pending) > 0 {
		return "", fmt.Errorf("the database is behind this binary (%d pending migrations): "+
			"run `touchgrass migrate up` or start the server first", len(pending))
	}

	audit := service.NewAudit(store.NewServiceStore(db), store.NewAuditStore(db))
	users := service.NewUsers(store.NewUserStore(db), audit).
		WithInitialPasswordFile(initialPasswordFile(cfg.DB))

	return users.ResetWithOneTimePassword(ctx, username, cliActor())
}
