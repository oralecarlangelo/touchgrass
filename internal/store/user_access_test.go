package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

const (
	// testHash stands in for a bcrypt hash the store never inspects.
	testHash = "hash"
	// testScopedName is the scoped fixture account.
	testScopedName = "scoped"
	// testNoService names a service that does not exist.
	testNoService = "no-such-service"
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

// openScopeDB returns a migrated database with the generic services and
// a user store on it.
func openScopeDB(t *testing.T) (*DB, *UserStore) {
	t.Helper()

	db := openTestDB(t)
	seedGenericServices(t, db)

	return db, NewUserStore(db)
}

// createUser stores one fixture user or fails the test.
func createUser(t *testing.T, users *UserStore, user model.User) model.User {
	t.Helper()

	user.PasswordHash = testHash

	created, err := users.Create(context.Background(), user, testGranter)
	if err != nil {
		t.Fatalf("Create(%s) error = %v, want nil", user.Username, err)
	}

	return created
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

// countGrants counts user_services rows for a user, or for a service
// when byService is set.
func countGrants(t *testing.T, db *DB, byService bool, value any) int {
	t.Helper()

	query := `SELECT COUNT(*) FROM user_services WHERE user_id = ?`
	if byService {
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

	_, users := openScopeDB(t)
	ctx := context.Background()

	created := createUser(t, users, model.User{
		Username: testScopedName, Role: model.RoleViewer,
		Services: []string{testfixture.GenericOther, testfixture.GenericGranted, testfixture.GenericOther},
	})

	want := []string{testfixture.GenericGranted, testfixture.GenericOther}
	if created.AllServices || !slices.Equal(created.Services, want) {
		t.Fatalf("Create() = %+v, want scoped to %v (sorted, de-duplicated)", created, want)
	}

	for name, read := range map[string]func() (model.User, error){
		"ByID":       func() (model.User, error) { return users.ByID(ctx, created.ID) },
		"ByUsername": func() (model.User, error) { return users.ByUsername(ctx, testScopedName) },
	} {
		got, err := read()
		if err != nil {
			t.Fatalf("%s() error = %v, want nil", name, err)
		}

		if got.AllServices || !slices.Equal(got.Services, want) {
			t.Errorf("%s() = %+v, want scoped to %v", name, got, want)
		}
	}
}

func TestUserStoreListCarriesScope(t *testing.T) {
	t.Parallel()

	_, users := openScopeDB(t)

	createUser(t, users, model.User{
		Username: testScopedName, Role: model.RoleViewer, Services: []string{testfixture.GenericGranted},
	})
	createUser(t, users, model.User{Username: "everywhere", Role: model.RoleViewer, AllServices: true})
	createUser(t, users, model.User{Username: "unassigned", Role: model.RoleViewer})

	listed, err := users.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(listed) != 3 {
		t.Fatalf("List() = %+v, want 3 users", listed)
	}

	if scoped := listed[0]; scoped.AllServices || !slices.Equal(scoped.Services, []string{testfixture.GenericGranted}) {
		t.Errorf("scoped = %+v, want the granted service", scoped)
	}

	for _, user := range listed[1:] {
		if user.Services == nil || len(user.Services) != 0 {
			t.Errorf("%s services = %v, want an empty, non-nil list", user.Username, user.Services)
		}
	}

	if !listed[1].AllServices || listed[2].AllServices {
		t.Errorf("List() = %+v, want everywhere on all services and unassigned on none", listed)
	}
}

func TestUserStoreDeleteCascadesGrants(t *testing.T) {
	t.Parallel()

	db, users := openScopeDB(t)
	ctx := context.Background()

	leaver := createUser(t, users, model.User{
		Username: "leaver", Role: model.RoleViewer, Services: []string{testfixture.GenericGranted},
	})
	stayer := createUser(t, users, model.User{
		Username: "stayer", Role: model.RoleViewer, Services: []string{testfixture.GenericGranted},
	})

	if err := users.Delete(ctx, leaver.ID); err != nil {
		t.Fatalf("Delete() error = %v, want nil", err)
	}

	if got := countGrants(t, db, false, leaver.ID); got != 0 {
		t.Errorf("grants after deleting the user = %d, want 0 (cascade)", got)
	}

	if got := countGrants(t, db, false, stayer.ID); got != 1 {
		t.Errorf("another user's grants = %d, want 1 left alone", got)
	}
}

func TestUserStoreSetAccess(t *testing.T) {
	t.Parallel()

	db, users := openScopeDB(t)
	ctx := context.Background()

	created := createUser(t, users, model.User{Username: testScopedName, Role: model.RoleViewer, AllServices: true})

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

	if _, err := users.SetAccess(ctx, created.ID+99, model.UserAccess{
		Role: model.RoleViewer, AllServices: true,
	}, testGranter); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("SetAccess(unknown user) error = %v, want ErrUserNotFound", err)
	}
}

func TestUserStoreSetAccessAllServicesClearsGrants(t *testing.T) {
	t.Parallel()

	db, users := openScopeDB(t)
	ctx := context.Background()

	created := createUser(t, users, model.User{
		Username: testScopedName, Role: model.RoleViewer,
		Services: []string{testfixture.GenericGranted, testfixture.GenericOther},
	})

	// A list sent along with all services is ignored, not stored.
	if _, err := users.SetAccess(ctx, created.ID, model.UserAccess{
		Role: model.RoleAdmin, AllServices: true, Services: []string{testfixture.GenericGranted},
	}, testGranter); err != nil {
		t.Fatalf("SetAccess(admin, all) error = %v, want nil", err)
	}

	got, err := users.ByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("ByID() error = %v, want nil", err)
	}

	if got.Role != model.RoleAdmin || !got.AllServices || len(got.Services) != 0 {
		t.Errorf("ByID() = %+v, want an all-services admin", got)
	}

	if count := countGrants(t, db, false, created.ID); count != 0 {
		t.Errorf("grants after all services = %d, want 0", count)
	}
}

func TestUserStoreSchemaRefusesBadAccess(t *testing.T) {
	t.Parallel()

	_, users := openScopeDB(t)
	ctx := context.Background()

	refused := []model.User{
		{Username: "scoped-admin", Role: model.RoleAdmin},
		{Username: "owner", Role: model.Role("owner"), AllServices: true},
		{Username: "ghost", Role: model.RoleViewer, Services: []string{testNoService}},
		{Username: "both", Role: model.RoleViewer, AllServices: true, Services: []string{testfixture.GenericGranted}},
	}

	for _, user := range refused {
		user.PasswordHash = testHash

		if _, err := users.Create(ctx, user, testGranter); err == nil {
			t.Errorf("Create(%s) error = nil, want a refusal", user.Username)
		}

		// A refused create leaves nothing behind, grants included.
		if _, err := users.ByUsername(ctx, user.Username); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("ByUsername(%s) error = %v, want the failed create rolled back", user.Username, err)
		}
	}

	// The schema already admits the reserved role; the service layer is
	// what keeps it from being assigned.
	createUser(t, users, model.User{Username: "reserved", Role: model.RoleDeveloper, AllServices: true})
}

func TestUserStoreSchemaRefusesAScopedAdmin(t *testing.T) {
	t.Parallel()

	_, users := openScopeDB(t)
	ctx := context.Background()

	viewer := createUser(t, users, model.User{
		Username: testScopedName, Role: model.RoleViewer, Services: []string{testfixture.GenericGranted},
	})

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

	_, users := openScopeDB(t)

	missing, err := users.MissingServices(context.Background(),
		[]string{testfixture.GenericGranted, testNoService, testfixture.GenericOther, "gone", testNoService})
	if err != nil {
		t.Fatalf("MissingServices() error = %v, want nil", err)
	}

	if !slices.Equal(missing, []string{testNoService, "gone"}) {
		t.Errorf("MissingServices() = %v, want [%s gone]", missing, testNoService)
	}

	none, err := users.MissingServices(context.Background(), []string{})
	if err != nil || len(none) != 0 {
		t.Errorf("MissingServices(empty) = %v, %v, want none", none, err)
	}
}

func TestServiceDeleteClearsGrants(t *testing.T) {
	t.Parallel()

	db, users := openScopeDB(t)
	ctx := context.Background()
	services := NewServiceStore(db)

	created := createUser(t, users, model.User{
		Username: testScopedName, Role: model.RoleViewer,
		Services: []string{testfixture.GenericGranted, testfixture.GenericOther},
	})

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
	seedOneGenericService(t, services, testfixture.GenericOther)

	if count := countGrants(t, db, true, testfixture.GenericOther); count != 0 {
		t.Errorf("grants on the re-created service = %d, want 0", count)
	}
}

// seedOneGenericService re-creates one of the generic services.
func seedOneGenericService(t *testing.T, services *ServiceStore, id string) {
	t.Helper()

	for _, service := range testfixture.GenericServices() {
		if service.ID != id {
			continue
		}

		if err := services.Create(context.Background(), service); err != nil {
			t.Fatalf("Create(%s) error = %v, want nil", service.ID, err)
		}
	}
}

func TestServiceByIDs(t *testing.T) {
	t.Parallel()

	db, _ := openScopeDB(t)
	services := NewServiceStore(db)

	got, err := services.ByIDs(context.Background(),
		[]string{testfixture.GenericOther, testNoService, testfixture.GenericGranted})
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
