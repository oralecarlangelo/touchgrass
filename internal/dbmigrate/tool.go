// Package dbmigrate reads the schema migration state of a monitored
// application: what its database records as applied, what its running
// build ships, and how the two differ.
//
// The package knows no application. Each migration tool (Prisma first)
// is a Tool: it says where its files live, how to read its history
// from a database, and which command applies it. Everything else, the
// comparison and the verdict, is shared and tool-neutral.
package dbmigrate

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"
)

// Errors a Tool or the registry reports.
var (
	// ErrUnknownTool reports a tool name that is not registered.
	ErrUnknownTool = errors.New("dbmigrate: unknown migration tool")
	// ErrUnsupportedDatabase reports a database URL the tool cannot read.
	ErrUnsupportedDatabase = errors.New("dbmigrate: database is not supported by this tool")
	// ErrInvalidURL reports a database URL that does not parse.
	ErrInvalidURL = errors.New("dbmigrate: database url is invalid")
	// ErrInvalidName reports a migration name outside the safe shape.
	ErrInvalidName = errors.New("dbmigrate: migration name is invalid")
	// ErrUnsupportedAction reports an action the tool does not offer.
	ErrUnsupportedAction = errors.New("dbmigrate: action is not supported by this tool")
)

// Action is a command a tool can run inside the application container.
type Action string

// Supported actions.
const (
	// ActionDeploy applies every pending migration.
	ActionDeploy Action = "deploy"
	// ActionResolveApplied marks a failed migration as applied.
	ActionResolveApplied Action = "resolve_applied"
	// ActionResolveRolledBack marks a failed migration as rolled back.
	ActionResolveRolledBack Action = "resolve_rolled_back"
	// ActionSeed runs the seed the application declares.
	ActionSeed Action = "seed"
)

// Record is one row of a tool's applied-migration history.
type Record struct {
	Name string
	// Checksum is empty when the tool recorded none for this row; the
	// migration then never reads as modified.
	Checksum  string
	StartedAt time.Time
	// FinishedAt equal to StartedAt means the tool records only when a
	// migration was applied, so no duration is reported.
	FinishedAt   *time.Time
	RolledBackAt *time.Time
	Steps        int
	Logs         string
}

// Shipped is one migration found in the running build.
type Shipped struct {
	Name     string
	Checksum string
}

// Source is what a tool found in the files of a running build.
type Source struct {
	Migrations []Shipped
	// SeedCommand is the seed the build declares, or "".
	SeedCommand string
}

// Files is a set of file contents keyed by slash-separated path
// relative to the directory they were read from.
type Files map[string][]byte

// Layout says which files of a build a tool needs, relative to the
// application's working directory.
type Layout struct {
	// MigrationsDir is the directory that holds the migrations.
	MigrationsDir string
	// Manifests are single files read beside it (for example the
	// package manifest that declares a seed).
	Manifests []string
}

// Target is a parsed database URL with the credential removed.
type Target struct {
	// Label is safe to show: host, port, database, and namespace.
	Label string
}

// Tool is one migration tool. Implementations hold no application
// knowledge and no state between calls.
type Tool interface {
	// Name is the stored identifier, for example "prisma".
	Name() string
	// Layout returns where the tool keeps its files by default.
	Layout() Layout
	// Describe validates a database URL and returns its redacted
	// target. It never connects.
	Describe(databaseURL string) (Target, error)
	// History reads the applied-migration record. A database the tool
	// has never migrated returns an empty history, not an error.
	History(ctx context.Context, databaseURL string) ([]Record, error)
	// Source finds the migrations and the seed in a build's files.
	// Migration paths are relative to the migrations directory.
	Source(migrations, manifests Files) (Source, error)
	// Command returns the arguments appended to the command prefix for
	// an action. Name is the migration an action concerns, or "".
	Command(action Action, name string) ([]string, error)
	// DefaultCommand is the command prefix used when none is stored.
	DefaultCommand() []string
}

// nameRe is the only migration-name shape accepted in a command line.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)

// ValidName reports whether name is safe to pass as one argument.
func ValidName(name string) bool {
	return nameRe.MatchString(name)
}

// Registry is the set of tools a deployment offers.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry indexes tools by name.
func NewRegistry(tools ...Tool) *Registry {
	index := make(map[string]Tool, len(tools))

	for _, tool := range tools {
		index[tool.Name()] = tool
	}

	return &Registry{tools: index}
}

// Get returns the named tool or ErrUnknownTool.
func (r *Registry) Get(name string) (Tool, error) {
	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTool, name)
	}

	return tool, nil
}

// Names lists the registered tools in a stable order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))

	for name := range r.tools {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}
