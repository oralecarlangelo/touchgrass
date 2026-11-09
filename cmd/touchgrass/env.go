package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/user"
	"strings"

	"github.com/oralecarlangelo/touchgrass/internal/config"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/probe"
	"github.com/oralecarlangelo/touchgrass/internal/secret"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const envUsageText = `Usage: touchgrass env <import|set|apply> --service <id> [options]

Subcommands:
  import [--replace]              read the configured target file into the store as applied entries
  set [--secret] KEY=VALUE        stage one entry (does not touch the file)
  apply [--summary TEXT]          render, validate, write, restart, probe; rolls back on failure

Requires TOUCHGRASS_ENV_KEY_FILE (or TOUCHGRASS_ENV_KEY) and the usual TOUCHGRASS_DB.
`

// envWiring bundles the dependencies the env service needs.
type envWiring struct {
	db       *store.DB
	cfg      config.Config
	services *store.ServiceStore
	audit    *service.Audit
	prober   service.Prober
	emit     func(model.Event)
	logger   *slog.Logger
}

// newEnvService loads the env key (disabled when absent, fatal when
// invalid), builds the service, and fails orphaned runs.
func newEnvService(ctx context.Context, wiring envWiring) (*service.Env, error) {
	key, err := secret.Load(wiring.cfg.EnvKeyFile, wiring.cfg.EnvKey)
	if err != nil && !errors.Is(err, secret.ErrKeyMissing) {
		return nil, fmt.Errorf("loading env key: %w", err)
	}

	if key == nil {
		wiring.logger.Info("env pillar disabled", "reason", "no "+secret.KeyFileEnvVar+" or "+secret.KeyEnvVar)
	}

	env := service.NewEnv(service.EnvConfig{
		Services: wiring.services, Store: store.NewEnvStore(wiring.db), Audit: wiring.audit, Key: key,
		Prober: wiring.prober, Emit: wiring.emit, Logger: wiring.logger,
	})

	if err := env.Reconcile(ctx); err != nil {
		return nil, fmt.Errorf("reconciling env runs: %w", err)
	}

	return env, nil
}

// runEnv dispatches the env subcommands.
func runEnv(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		if _, err := io.WriteString(os.Stdout, envUsageText); err != nil {
			return fmt.Errorf("writing usage: %w", err)
		}

		return nil
	}

	switch args[0] {
	case "import":
		return runEnvImport(args[1:])
	case "set":
		return runEnvSet(args[1:])
	case "apply":
		return runEnvApply(args[1:])
	default:
		return fmt.Errorf("unknown env subcommand %q: want import, set, or apply", args[0])
	}
}

// envCommand is one opened env CLI context.
type envCommand struct {
	env     *service.Env
	service model.Service
	actor   string
	close   func()
}

// openEnv parses the common --service flag, opens the store, and builds
// the env service without background loops.
func openEnv(name string, args []string, register func(*flag.FlagSet)) (*envCommand, []string, error) {
	fs := flag.NewFlagSet("env "+name, flag.ContinueOnError)
	serviceID := fs.String("service", "", "managed service id (required)")

	if register != nil {
		register(fs)
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, nil, nil
		}

		return nil, nil, fmt.Errorf("parsing env %s flags: %w", name, err)
	}

	if *serviceID == "" {
		return nil, nil, errors.New("--service is required")
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}

	db, err := store.Open(cfg.DB)
	if err != nil {
		return nil, nil, err
	}

	closeDB := func() {
		if err := db.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "touchgrass: closing database: %v\n", err)
		}
	}

	ctx := context.Background()
	services := store.NewServiceStore(db)

	def, err := services.Get(ctx, *serviceID)
	if err != nil {
		closeDB()

		return nil, nil, err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	audit := service.NewAudit(services, store.NewAuditStore(db))

	env, err := newEnvService(ctx, envWiring{
		db: db, cfg: cfg, services: services, audit: audit, prober: probe.New(), logger: logger,
	})
	if err != nil {
		closeDB()

		return nil, nil, err
	}

	if !env.Enabled() {
		closeDB()

		return nil, nil, fmt.Errorf("env key is not configured: set %s", secret.KeyFileEnvVar)
	}

	return &envCommand{env: env, service: def, actor: cliActor(), close: closeDB}, fs.Args(), nil
}

// cliActor attributes CLI runs to the invoking OS user.
func cliActor() string {
	if current, err := user.Current(); err == nil && current.Username != "" {
		return "cli:" + current.Username
	}

	return "cli"
}

// runEnvImport reads the target file into the store.
func runEnvImport(args []string) error {
	var replace bool

	cmd, rest, err := openEnv("import", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&replace, "replace", false, "replace entries that already exist")
	})
	if err != nil || cmd == nil {
		return err
	}

	defer cmd.close()

	if len(rest) > 0 {
		return errors.New("env import takes no positional arguments")
	}

	result, err := cmd.env.Import(context.Background(), cmd.service.ID, replace, cmd.actor)
	if err != nil {
		return err
	}

	return outputLine(fmt.Sprintf("imported %d keys (%d skipped, %d guessed secret)",
		result.Imported, result.Skipped, result.SecretGuessed))
}

// runEnvSet stages one KEY=VALUE entry.
func runEnvSet(args []string) error {
	var isSecret bool

	cmd, rest, err := openEnv("set", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&isSecret, "secret", false, "mask the value everywhere")
	})
	if err != nil || cmd == nil {
		return err
	}

	defer cmd.close()

	if len(rest) != 1 {
		return errors.New("env set takes exactly one KEY=VALUE argument")
	}

	key, value, found := strings.Cut(rest[0], "=")
	if !found {
		return errors.New("env set argument must be KEY=VALUE")
	}

	entry, err := cmd.env.Stage(context.Background(), cmd.service.ID, key, value, isSecret, cmd.actor)
	if err != nil {
		return err
	}

	return outputLine(fmt.Sprintf("%s %s (secret=%t)", entry.State, entry.Key, entry.IsSecret))
}

// runEnvApply applies pending changes and waits for the run to finish.
func runEnvApply(args []string) error {
	var summary string

	cmd, rest, err := openEnv("apply", args, func(fs *flag.FlagSet) {
		fs.StringVar(&summary, "summary", "", "revision summary")
	})
	if err != nil || cmd == nil {
		return err
	}

	defer cmd.close()

	if len(rest) > 0 {
		return errors.New("env apply takes no positional arguments")
	}

	ctx := context.Background()

	runID, err := cmd.env.Apply(ctx, cmd.service.ID, cmd.service.Name, summary, cmd.actor)
	if err != nil {
		return err
	}

	run, err := cmd.env.AwaitRun(ctx, cmd.service.ID, runID)
	if err != nil {
		return err
	}

	for _, step := range run.Steps {
		if err := outputLine(step.Step + ": " + step.Message); err != nil {
			return err
		}
	}

	if run.State != model.EnvRunSucceeded {
		return fmt.Errorf("env apply %s %s", runID, run.State)
	}

	return outputLine("env apply " + runID + " succeeded")
}
