package http

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/dbmigrate"
	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/secret"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// newTestMigrations builds the migrations pillar on the harness store.
// It shares the env key option: without a key no setup can be saved.
func newTestMigrations(
	t *testing.T,
	db *store.DB,
	services *store.ServiceStore,
	audit *service.Audit,
	opts testServerOptions,
) *service.Migrations {
	t.Helper()

	var key *secret.Key

	if !opts.envKeyMissing {
		loaded, err := secret.Load("", testEnvKeyHex)
		if err != nil {
			t.Fatalf("secret.Load() error = %v, want nil", err)
		}

		key = loaded
	}

	engine := opts.migrationDocker
	if engine == nil {
		engine = &stubMigrationDocker{}
	}

	tools := dbmigrate.NewRegistry(dbmigrate.NewPrisma())
	if opts.migrationTool != nil {
		tools = dbmigrate.NewRegistry(opts.migrationTool)
	}

	migrations := service.NewMigrations(service.MigrationsConfig{
		Services: services,
		Configs:  store.NewMigrationConfigStore(db),
		Runs:     store.NewMigrationRunStore(db),
		Audit:    audit,
		Key:      key,
		Tools:    tools,
		Docker:   engine,
	})

	t.Cleanup(migrations.Close)

	return migrations
}

// stubMigrationDocker is an engine with a fixed set of containers and
// files. Exec records the command and writes a canned output.
type stubMigrationDocker struct {
	containers []docker.Container
	workdir    string
	files      map[string]map[string][]byte
	exitCode   int
	output     string
	// release, when set, blocks Exec until it is closed.
	release chan struct{}

	mutex sync.Mutex
	execs []docker.ExecSpec
}

func (s *stubMigrationDocker) List(_ context.Context) ([]docker.Container, error) {
	return s.containers, nil
}

func (s *stubMigrationDocker) Workdir(_ context.Context, _ string) (string, error) {
	if s.workdir == "" {
		return "/app", nil
	}

	return s.workdir, nil
}

func (s *stubMigrationDocker) ReadFiles(_ context.Context, _, containerPath string) (map[string][]byte, error) {
	files, ok := s.files[containerPath]
	if !ok {
		return nil, docker.ErrPathNotFound
	}

	return files, nil
}

func (s *stubMigrationDocker) Exec(ctx context.Context, _ string, spec docker.ExecSpec, output io.Writer) (int, error) {
	s.mutex.Lock()
	s.execs = append(s.execs, spec)
	s.mutex.Unlock()

	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}

	if _, err := io.WriteString(output, s.output); err != nil {
		return 0, err
	}

	return s.exitCode, nil
}

// executed returns the commands Exec has seen.
func (s *stubMigrationDocker) executed() []docker.ExecSpec {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return append([]docker.ExecSpec{}, s.execs...)
}

// stubMigrationTool is a made-up migration tool with a fixed history,
// so handler tests need no database and do not lean on Prisma.
type stubMigrationTool struct {
	records []dbmigrate.Record
}

func (*stubMigrationTool) Name() string { return "ledger" }

func (*stubMigrationTool) Layout() dbmigrate.Layout {
	return dbmigrate.Layout{MigrationsDir: "changes", Manifests: []string{"ledger.json"}}
}

func (*stubMigrationTool) DefaultCommand() []string { return []string{"ledger"} }

func (*stubMigrationTool) Describe(_ string) (dbmigrate.Target, error) {
	return dbmigrate.Target{Label: "db.internal/billing"}, nil
}

func (s *stubMigrationTool) History(_ context.Context, _ string) ([]dbmigrate.Record, error) {
	return s.records, nil
}

func (*stubMigrationTool) Source(migrations, manifests dbmigrate.Files) (dbmigrate.Source, error) {
	source := dbmigrate.Source{SeedCommand: string(manifests["ledger.json"])}

	for name, content := range migrations {
		source.Migrations = append(source.Migrations, dbmigrate.Shipped{Name: name, Checksum: string(content)})
	}

	return source, nil
}

func (*stubMigrationTool) Command(action dbmigrate.Action, name string) ([]string, error) {
	switch action {
	case dbmigrate.ActionDeploy:
		return []string{"up"}, nil
	case dbmigrate.ActionSeed:
		return []string{"seed"}, nil
	case dbmigrate.ActionResolveApplied, dbmigrate.ActionResolveRolledBack:
		return []string{"mark", string(action), name}, nil
	default:
		return nil, dbmigrate.ErrUnsupportedAction
	}
}
