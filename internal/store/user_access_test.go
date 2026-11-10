package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// seedGenericServices adds the generic two-service fixture.
func seedGenericServices(t *testing.T, db *DB) {
	t.Helper()

	services := NewServiceStore(db)

	for _, service := range testfixture.GenericServices() {
		if err := services.Create(context.Background(), service); err != nil {
			t.Fatalf("Create(%s) error = %v, want nil", service.ID, err)
		}
	}
}

// grantedBy reads who granted one service to one user.
func grantedBy(t *testing.T, db *DB, userID int64, serviceID string) string {
	t.Helper()

	var granter string

	if err := db.sql.QueryRowContext(context.Background(),
		`SELECT granted_by FROM user_services WHERE user_id = ? AND service_id = ?`, userID, serviceID,
	).Scan(&granter); err != nil {
		t.Fatalf("reading grant %d/%s: %v", userID, serviceID, err)
	}

	return granter
}

// countGrants counts the user_services rows matching one column value.
func countGrants(t *testing.T, db *DB, column string, value any) int {
	t.Helper()

	query := `SELECT COUNT(*) FROM user_services WHERE user_id = ?`
	if column == "service_id" {
		query = `SELECT COUNT(*) FROM user_services WHERE service_id = ?`
	}

	var count int

	if err := db.sql.QueryRowContext(context.Background(), query, value).Scan(&count); err != nil {
		t.Fatalf("counting grants: %v", err)
	}

	return count
}

func TestUserStoreScopeRoundTrip(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	seedGenericServices(t, db)

	ctx := context.Background()
	users := NewUserStore(db)

	created, err := users.Create(ctx, model.User{
		Username: "scoped", PasswordHash: "hash", Role: model.RoleViewer,
		Services: []string{testfixture.GenericOther, testfixture.GenericGranted, testfixture.GenericOther},
	}, testGranter)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	want := []string{testfixture.GenericGranted, testfixture.GenericOther}
	if created.AllServices || !slices.Equal(created.Services, want) {
		t.Fatalf("Create() = %+v, want scoped to %v (sorted, de-duplicated)", created, want)
	}

	for name, read := range map[string]func() (model.User, error){
		"ByID":       func() (model.User, error) { return users.ByID(ctx, created.ID) },
		"ByUsername": func() (model.User, error) { return users.ByUsername(ctx, "scoped") },
	} {
		got, err := read()
		if err != nil {
			t.Fatalf("%s() error = %v, want nil", name, err)
		}

		if got.AllServices || !slices.Equal(got.Services, want) {
			t.Errorf("%s() = %+v, want scoped to %v", name, got, want)
		}
	}

	everywhere, err := users.Create(ctx, model.User{
		Username: "everywhere", PasswordHash: "hash", Role: model.RoleViewer, AllServices: true,
	}, testGranter)
	if err != nil {
		t.Fatalf("Create(everywhere) error = %v, want nil", err)
	}

	listed, err := users.List(ctx, 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(listed) != 2 || !slices.Equal(listed[0].Services, want) ||
		!listed[1].AllServices || len(listed[1].Services) != 0 || listed[1].Services == nil {
		t.Errorf("List() = %+v, want scoped then everywhere with an empty, non-nil list", listed)
	}

	if _, err := users.Create(ctx, model.User{
		Username: "both", PasswordHash: "hash", Role: model.RoleViewer,
		AllServices: true, Services: []string{testfixture.GenericGranted},
	}, testGranter); err == nil {
		t.Error("Create(all services plus a list) error = nil, want a refusal")
	}

	if err := users.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v, want nil", err)
	}

	if got := countGrants(t, db, "user_id", created.ID); got != 0 {
		t.Errorf("grants after deleting the user = %d, want 0 (cascade)", got)
	}

	if _, err := users.ByID(ctx, everywhere.ID); err != nil {
		t.Errorf("ByID(everywhere) after deleting another user error = %v, want nil", err)
	}
}

func TestUserStoreSetAccess(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	seedGenericServices(t, db)

	ctx := context.Background()
	users := NewUserStore(db)

	created, err := users.Create(ctx, model.User{
		Username: "dev", PasswordHash: "hash", Role: model.RoleViewer, AllServices: true,
	}, testGranter)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	services, err := users.SetAccess(ctx, created.ID, model.UserAccess{
		Role: model.RoleViewer, Services: []string{testfixture.GenericGranted},
	}, "first-admin")
	if err != nil {
		t.Fatalf("SetAccess(one service) error = %v, want nil", err)
	}

	if !slices.Equal(services, []string{testfixture.GenericGranted}) {
		t.Errorf("SetAccess() services = %v, want the granted one", services)
	}

	// Widening the list keeps the first grant's attribution.
	if _, err := users.SetAccess(ctx, created.ID, model.UserAccess{
		Role: model.RoleViewer, Services: []string{testfixture.GenericOther, testfixture.GenericGranted},
	}, "second-admin"); err != nil {
		t.Fatalf("SetAccess(two services) error = %v, want nil", err)
	}

	if got := grantedBy(t, db, created.ID, testfixture.GenericGranted); got != "first-admin" {
		t.Errorf("kept grant granted_by = %q, want first-admin", got)
	}

	if got := grantedBy(t, db, created.ID, testfixture.GenericOther); got != "second-admin" {
		t.Errorf("new grant granted_by = %q, want second-admin", got)
	}

	// Back to all services: every grant goes.
	if _, err := users.SetAccess(ctx, created.ID, model.UserAccess{
		Role: model.RoleAdmin, AllServices: true, Services: []string{testfixture.GenericGranted},
	}, "second-admin"); err != nil {
		t.Fatalf("SetAccess(admin, all) error = %v, want nil", err)
	}

	got, err := users.ByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("ByID() error = %v, want nil", err)
	}

	if got.Role != model.RoleAdmin || !got.AllServices || len(got.Services) != 0 {
		t.Errorf("ByID() = %+v, want an all-services admin", got)
	}

	if count := countGrants(t, db, "user_id", created.ID); count != 0 {
		t.Errorf("grants after all services = %d, want 0", count)
	}

	if _, err := users.SetAccess(ctx, created.ID+99, model.UserAccess{
		Role: model.RoleViewer, AllServices: true,
	}, testGranter); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("SetAccess(unknown user) error = %v, want ErrUserNotFound", err)
	}
}

func TestUserStoreSchemaRefusesBadAccess(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	seedGenericServices(t, db)

	ctx := context.Background()
	users := NewUserStore(db)

	if _, err := users.Create(ctx, model.User{
		Username: "scoped-admin", PasswordHash: "hash", Role: model.RoleAdmin,
	}, testGranter); err == nil {
		t.Error("Create(scoped admin) error = nil, want the CHECK to refuse it")
	}

	if _, err := users.Create(ctx, model.User{
		Username: "owner", PasswordHash: "hash", Role: model.Role("owner"), AllServices: true,
	}, testGranter); err == nil {
		t.Error("Create(unknown role) error = nil, want the CHECK to refuse it")
	}

	// The schema already admits the reserved role; the service layer is
	// what keeps it from being assigned.
	if _, err := users.Create(ctx, model.User{
		Username: "reserved", PasswordHash: "hash", Role: model.RoleDeveloper, AllServices: true,
	}, testGranter); err != nil {
		t.Errorf("Create(developer) error = %v, want the schema to admit it", err)
	}

	if _, err := users.Create(ctx, model.User{
		Username: "ghost", PasswordHash: "hash", Role: model.RoleViewer, Services: []string{"no-such-service"},
	}, testGranter); err == nil {
		t.Error("Create(grant on an unknown service) error = nil, want the foreign key to refuse it")
	}

	if _, err := users.ByUsername(ctx, "ghost"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("ByUsername(ghost) error = %v, want the failed create rolled back", err)
	}

	viewer, err := users.Create(ctx, model.User{
		Username: "viewer", PasswordHash: "hash", Role: model.RoleViewer,
		Services: []string{testfixture.GenericGranted},
	}, testGranter)
	if err != nil {
		t.Fatalf("Create(viewer) error = %v, want nil", err)
	}

	if _, err := users.SetAccess(ctx, viewer.ID, model.UserAccess{
		Role: model.RoleAdmin, Services: []string{testfixture.GenericGranted},
	}, testGranter); err == nil {
		t.Error("SetAccess(scoped admin) error = nil, want the CHECK to refuse it")
	}

	got, err := users.ByID(ctx, viewer.ID)
	if err != nil {
		t.Fatalf("ByID() error = %v, want nil", err)
	}

	if got.Role != model.RoleViewer || !slices.Equal(got.Services, []string{testfixture.GenericGranted}) {
		t.Errorf("ByID() after a refused change = %+v, want the viewer untouched", got)
	}
}

func TestUserStoreMissingServices(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	seedGenericServices(t, db)

	users := NewUserStore(db)

	missing, err := users.MissingServices(context.Background(),
		[]string{testfixture.GenericGranted, "nope", testfixture.GenericOther, "gone", "nope"})
	if err != nil {
		t.Fatalf("MissingServices() error = %v, want nil", err)
	}

	if !slices.Equal(missing, []string{"nope", "gone"}) {
		t.Errorf("MissingServices() = %v, want [nope gone]", missing)
	}

	none, err := users.MissingServices(context.Background(), []string{})
	if err != nil || len(none) != 0 {
		t.Errorf("MissingServices(empty) = %v, %v, want none", none, err)
	}
}

func TestServiceDeleteClearsGrants(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	seedGenericServices(t, db)

	ctx := context.Background()
	users := NewUserStore(db)
	services := NewServiceStore(db)

	created, err := users.Create(ctx, model.User{
		Username: "dev", PasswordHash: "hash", Role: model.RoleViewer,
		Services: []string{testfixture.GenericGranted, testfixture.GenericOther},
	}, testGranter)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if err := services.Delete(ctx, testfixture.GenericOther); err != nil {
		t.Fatalf("Delete(service) error = %v, want nil", err)
	}

	got, err := users.ByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("ByID() error = %v, want nil", err)
	}

	if !slices.Equal(got.Services, []string{testfixture.GenericGranted}) {
		t.Errorf("services after deleting one = %v, want only the survivor", got.Services)
	}

	// A service re-created under the same id must not inherit grants.
	for _, service := range testfixture.GenericServices() {
		if service.ID != testfixture.GenericOther {
			continue
		}

		if err := services.Create(ctx, service); err != nil {
			t.Fatalf("re-Create(%s) error = %v, want nil", service.ID, err)
		}
	}

	if count := countGrants(t, db, "service_id", testfixture.GenericOther); count != 0 {
		t.Errorf("grants on the re-created service = %d, want 0", count)
	}
}

func TestServiceByIDs(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	seedGenericServices(t, db)

	services := NewServiceStore(db)

	got, err := services.ByIDs(context.Background(), []string{testfixture.GenericOther, "nope", testfixture.GenericGranted})
	if err != nil {
		t.Fatalf("ByIDs() error = %v, want nil", err)
	}

	if len(got) != 2 || got[0].ID != testfixture.GenericGranted || got[1].ID != testfixture.GenericOther {
		t.Errorf("ByIDs() = %+v, want the two generic services ordered by id", got)
	}

	none, err := services.ByIDs(context.Background(), []string{})
	if err != nil || len(none) != 0 {
		t.Errorf("ByIDs(empty) = %v, %v, want nothing", none, err)
	}
}
