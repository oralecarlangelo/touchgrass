package service

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// openScopedUsers wires a Users service on a database that also holds
// the generic two-service fixture.
func openScopedUsers(t *testing.T) (*Users, *store.DB) {
	t.Helper()

	users, db := openUsers(t)
	services := store.NewServiceStore(db)

	for _, fixture := range testfixture.GenericServices() {
		if err := services.Create(context.Background(), fixture); err != nil {
			t.Fatalf("Create(%s) error = %v, want nil", fixture.ID, err)
		}
	}

	return users, db
}

// latestAuditDetail returns the newest audit row's action and detail.
func latestAuditDetail(t *testing.T, db *store.DB) (string, string) {
	t.Helper()

	entries, err := store.NewAuditStore(db).List(context.Background(), "", 1)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}

	return entries[0].Action, entries[0].Detail
}

func TestUsersCreateScoped(t *testing.T) {
	t.Parallel()

	users, db := openScopedUsers(t)
	ctx := context.Background()

	created, err := users.Create(ctx, CreateUserInput{
		Username: testDevUsername, Password: testUserPassword, Role: model.RoleViewer,
		Services: []string{testfixture.GenericOther, testfixture.GenericGranted, testfixture.GenericOther},
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	want := []string{testfixture.GenericGranted, testfixture.GenericOther}
	if created.AllServices || !slices.Equal(created.Services, want) || !created.MustChangePassword {
		t.Errorf("Create() = %+v, want a scoped viewer on a temporary password", created)
	}

	action, detail := latestAuditDetail(t, db)
	if action != model.AuditUserCreate ||
		detail != "created user dev (role viewer, services [billing-worker, docs-site])" {
		t.Errorf("audit = %s %q, want the role and scope recorded", action, detail)
	}

	looked, err := users.Lookup(ctx, testDevUsername)
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}

	if looked.AllServices || !slices.Equal(looked.Services, want) {
		t.Errorf("Lookup() = %+v, want the scope a session is built from", looked)
	}
}

func TestUsersAccessRejects(t *testing.T) {
	t.Parallel()

	users, _ := openScopedUsers(t)
	ctx := context.Background()

	existing, err := users.Create(ctx, CreateUserInput{
		Username: testDevUsername, Password: testUserPassword, Role: model.RoleViewer, AllServices: true,
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	tests := []struct {
		name     string
		username string
		access   model.UserAccess
		wantErr  error
	}{
		{
			name: "the reserved developer role", username: "new-developer",
			access:  model.UserAccess{Role: model.RoleDeveloper, AllServices: true},
			wantErr: ErrInvalidInput,
		},
		{
			name: "an unknown role", username: "new-owner",
			access:  model.UserAccess{Role: model.Role("owner"), AllServices: true},
			wantErr: ErrInvalidInput,
		},
		{
			name: "an empty role", username: "new-roleless",
			access:  model.UserAccess{Role: model.Role(""), AllServices: true},
			wantErr: ErrInvalidInput,
		},
		{
			name: "an unknown service", username: "new-ghost",
			access:  model.UserAccess{Role: model.RoleViewer, Services: []string{testfixture.GenericGranted, "no-such-service"}},
			wantErr: ErrInvalidInput,
		},
		{
			name: "all services together with a list", username: "new-both",
			access:  model.UserAccess{Role: model.RoleViewer, AllServices: true, Services: []string{testfixture.GenericGranted}},
			wantErr: ErrInvalidInput,
		},
		{
			name: "a scoped admin", username: "new-scoped-admin",
			access:  model.UserAccess{Role: model.RoleAdmin, Services: []string{testfixture.GenericGranted}},
			wantErr: ErrConflict,
		},
		{
			name: "an admin with no services", username: "new-empty-admin",
			access:  model.UserAccess{Role: model.RoleAdmin},
			wantErr: ErrConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := users.SetAccess(ctx, existing.ID, tt.access, testAdminActor); !errors.Is(err, tt.wantErr) {
				t.Errorf("SetAccess() error = %v, want %v", err, tt.wantErr)
			}

			_, err := users.Create(ctx, CreateUserInput{
				Username: tt.username, Password: testUserPassword, Role: tt.access.Role,
				AllServices: tt.access.AllServices, Services: tt.access.Services,
			}, testAdminActor)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Create() error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	if _, err := users.SetAccess(ctx, existing.ID, model.UserAccess{
		Role: model.RoleViewer, AllServices: true,
	}, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("SetAccess(no actor) error = %v, want ErrInvalidInput", err)
	}

	if _, err := users.SetAccess(ctx, existing.ID+99, model.UserAccess{
		Role: model.RoleViewer, AllServices: true,
	}, testAdminActor); !errors.Is(err, store.ErrUserNotFound) {
		t.Errorf("SetAccess(unknown user) error = %v, want ErrUserNotFound", err)
	}

	if _, err := users.SetRole(ctx, existing.ID, model.RoleDeveloper, testAdminActor); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("SetRole(developer) error = %v, want ErrInvalidInput", err)
	}
}

// createDev stores the fixture viewer with every service.
func createDev(t *testing.T, users *Users) model.User {
	t.Helper()

	created, err := users.Create(context.Background(), CreateUserInput{
		Username: testDevUsername, Password: testUserPassword, Role: model.RoleViewer, AllServices: true,
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	return created
}

func TestUsersSetAccessRoundTrip(t *testing.T) {
	t.Parallel()

	users, db := openScopedUsers(t)
	ctx := context.Background()
	created := createDev(t, users)

	scoped, err := users.SetAccess(ctx, created.ID, model.UserAccess{
		Role: model.RoleViewer, Services: []string{testfixture.GenericGranted},
	}, testAdminActor)
	if err != nil {
		t.Fatalf("SetAccess(scoped) error = %v, want nil", err)
	}

	if scoped.AllServices || !slices.Equal(scoped.Services, []string{testfixture.GenericGranted}) {
		t.Errorf("SetAccess() = %+v, want scoped to the granted service", scoped)
	}

	if action, detail := latestAuditDetail(t, db); action != model.AuditUserUpdate ||
		detail != "set access of user dev: role viewer, services [billing-worker]" {
		t.Errorf("audit = %s %q, want the new access recorded", action, detail)
	}

	stored, err := users.Lookup(ctx, testDevUsername)
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}

	if stored.AllServices || !slices.Equal(stored.Services, []string{testfixture.GenericGranted}) {
		t.Errorf("Lookup() = %+v, want the stored scope", stored)
	}
}

// A viewer may be left with no service at all.
func TestUsersSetAccessToNoServices(t *testing.T) {
	t.Parallel()

	users, db := openScopedUsers(t)
	created := createDev(t, users)

	emptied, err := users.SetAccess(context.Background(), created.ID,
		model.UserAccess{Role: model.RoleViewer}, testAdminActor)
	if err != nil {
		t.Fatalf("SetAccess(no services) error = %v, want nil", err)
	}

	if emptied.AllServices || len(emptied.Services) != 0 {
		t.Errorf("SetAccess(no services) = %+v, want an empty scope", emptied)
	}

	if _, detail := latestAuditDetail(t, db); detail != "set access of user dev: role viewer, services []" {
		t.Errorf("audit detail = %q, want the empty list recorded", detail)
	}
}

// Promoting through the legacy role route widens the scope, since an
// admin is never scoped.
func TestUsersSetRoleAdminWidensScope(t *testing.T) {
	t.Parallel()

	users, db := openScopedUsers(t)
	ctx := context.Background()
	created := createDev(t, users)

	if _, err := users.SetAccess(ctx, created.ID, model.UserAccess{
		Role: model.RoleViewer, Services: []string{testfixture.GenericGranted},
	}, testAdminActor); err != nil {
		t.Fatalf("SetAccess(scoped) error = %v, want nil", err)
	}

	promoted, err := users.SetRole(ctx, created.ID, model.RoleAdmin, testAdminActor)
	if err != nil {
		t.Fatalf("SetRole(admin) error = %v, want nil", err)
	}

	if promoted.Role != model.RoleAdmin || !promoted.AllServices || len(promoted.Services) != 0 {
		t.Errorf("SetRole(admin) = %+v, want an all-services admin", promoted)
	}

	if _, detail := latestAuditDetail(t, db); detail != "changed role of user dev to admin (scope widened to all services)" {
		t.Errorf("audit detail = %q, want the widening recorded", detail)
	}
}

func TestUsersSetRoleKeepsScope(t *testing.T) {
	t.Parallel()

	users, _ := openScopedUsers(t)
	ctx := context.Background()

	created, err := users.Create(ctx, CreateUserInput{
		Username: testDevUsername, Password: testUserPassword, Role: model.RoleViewer,
		Services: []string{testfixture.GenericGranted},
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	same, err := users.SetRole(ctx, created.ID, model.RoleViewer, testAdminActor)
	if err != nil {
		t.Fatalf("SetRole(viewer) error = %v, want nil", err)
	}

	if same.AllServices || !slices.Equal(same.Services, []string{testfixture.GenericGranted}) {
		t.Errorf("SetRole(viewer) = %+v, want the scope untouched", same)
	}
}

// The last enabled admin cannot lose the admin role by any route, to
// any role or scope.
func TestUsersLastAdminGuardCoversEveryAccessChange(t *testing.T) {
	t.Parallel()

	users, _ := openScopedUsers(t)
	ctx := context.Background()

	admin, err := users.Create(ctx, CreateUserInput{
		Username: "solo", Password: testUserPassword, Role: model.RoleAdmin, AllServices: true,
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	demotions := map[string]model.UserAccess{
		"all-services viewer": {Role: model.RoleViewer, AllServices: true},
		"scoped viewer":       {Role: model.RoleViewer, Services: []string{testfixture.GenericGranted}},
		"viewer with nothing": {Role: model.RoleViewer},
	}

	for name, access := range demotions {
		if _, err := users.SetAccess(ctx, admin.ID, access, testAdminActor); !errors.Is(err, ErrConflict) {
			t.Errorf("SetAccess(last admin to %s) error = %v, want ErrConflict", name, err)
		}
	}

	if _, err := users.SetRole(ctx, admin.ID, model.RoleViewer, testAdminActor); !errors.Is(err, ErrConflict) {
		t.Errorf("SetRole(last admin to viewer) error = %v, want ErrConflict", err)
	}

	still, err := users.Lookup(ctx, "solo")
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}

	if still.Role != model.RoleAdmin || !still.AllServices {
		t.Errorf("Lookup() after refused demotions = %+v, want the admin untouched", still)
	}

	if _, err := users.Create(ctx, CreateUserInput{
		Username: "backup", Password: testUserPassword, Role: model.RoleAdmin, AllServices: true,
	}, testAdminActor); err != nil {
		t.Fatalf("Create(backup) error = %v, want nil", err)
	}

	demoted, err := users.SetAccess(ctx, admin.ID, demotions["scoped viewer"], testAdminActor)
	if err != nil {
		t.Fatalf("SetAccess(admin with a backup) error = %v, want nil", err)
	}

	if demoted.Role != model.RoleViewer || demoted.AllServices {
		t.Errorf("SetAccess() = %+v, want a scoped viewer", demoted)
	}
}

// EnsureAdmin repairs the bootstrap admin even when that row was left
// as a scoped viewer: it comes back as an admin with every service.
func TestUsersEnsureAdminResetsScope(t *testing.T) {
	t.Parallel()

	users, db := openScopedUsers(t)
	ctx := context.Background()
	rows := store.NewUserStore(db)

	if _, err := users.EnsureAdmin(ctx, testUserPassword); err != nil {
		t.Fatalf("EnsureAdmin() error = %v, want nil", err)
	}

	admin, err := rows.ByUsername(ctx, bootstrapAdmin)
	if err != nil {
		t.Fatalf("ByUsername(admin) error = %v, want nil", err)
	}

	// Leave the install with no admin at all, behind the service's back.
	if _, err := rows.SetAccess(ctx, admin.ID, model.UserAccess{
		Role: model.RoleViewer, Services: []string{testfixture.GenericGranted},
	}, "someone"); err != nil {
		t.Fatalf("SetAccess(store) error = %v, want nil", err)
	}

	bootstrap, err := users.EnsureAdmin(ctx, "another-horse-battery")
	if err != nil {
		t.Fatalf("EnsureAdmin(repair) error = %v, want nil", err)
	}

	if !bootstrap.Seeded {
		t.Errorf("EnsureAdmin(repair) = %+v, want Seeded", bootstrap)
	}

	repaired, err := users.Verify(ctx, bootstrapAdmin, "another-horse-battery")
	if err != nil {
		t.Fatalf("Verify(repaired) error = %v, want nil", err)
	}

	if repaired.Role != model.RoleAdmin || !repaired.AllServices || len(repaired.Services) != 0 {
		t.Errorf("repaired admin = %+v, want an admin with every service and no grants", repaired)
	}
}

func TestScopedServiceLists(t *testing.T) {
	t.Parallel()

	users, db := openScopedUsers(t)
	_ = users

	ctx := context.Background()
	services := store.NewServiceStore(db)
	audit := NewAudit(services, store.NewAuditStore(db))

	for _, record := range []model.AuditRecord{
		{ServiceID: testfixture.GenericGranted, Actor: testAdminActor, Action: model.AuditDeploy, Result: model.AuditSuccess, Detail: "granted"},
		{ServiceID: testfixture.GenericOther, Actor: testAdminActor, Action: model.AuditDeploy, Result: model.AuditSuccess, Detail: "other"},
		{Actor: testAdminActor, Action: model.AuditLogin, Result: model.AuditSuccess, Detail: "login from 10.0.0.9"},
	} {
		if _, err := audit.Record(ctx, record); err != nil {
			t.Fatalf("Record() error = %v, want nil", err)
		}
	}

	entries, err := audit.ListForServices(ctx, []string{testfixture.GenericGranted}, 0)
	if err != nil {
		t.Fatalf("ListForServices() error = %v, want nil", err)
	}

	if len(entries) != 1 || entries[0].Detail != "granted" {
		t.Errorf("ListForServices() = %+v, want only the granted service's row", entries)
	}

	inventory := NewInventory(services, stubLister{}, stubProber{}, slog.New(slog.DiscardHandler))

	views, err := inventory.ServicesIn(ctx, []string{testfixture.GenericGranted, "no-such-service"})
	if err != nil {
		t.Fatalf("ServicesIn() error = %v, want nil", err)
	}

	if len(views) != 1 || views[0].ID != testfixture.GenericGranted {
		t.Errorf("ServicesIn() = %+v, want only the granted service", views)
	}

	none, err := inventory.ServicesIn(ctx, []string{})
	if err != nil || len(none) != 0 {
		t.Errorf("ServicesIn(no services) = %+v, %v, want nothing", none, err)
	}
}

func TestOwnersServiceOf(t *testing.T) {
	t.Parallel()

	_, db := openScopedUsers(t)
	ctx := context.Background()
	owners := NewOwners(store.NewOwnerStore(db))

	notification, err := store.NewNotificationStore(db).Insert(ctx, testfixture.GenericGranted, "alert", "title", "body")
	if err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	got, err := owners.ServiceOf(ctx, model.ResourceNotification, notification.ID)
	if err != nil || got != testfixture.GenericGranted {
		t.Errorf("ServiceOf() = %q, %v, want the granted service", got, err)
	}

	if _, err := owners.ServiceOf(ctx, model.ResourceNotification, notification.ID+99); !errors.Is(err, ErrNoOwner) {
		t.Errorf("ServiceOf(missing) error = %v, want ErrNoOwner", err)
	}

	if _, err := owners.ServiceOf(ctx, model.ResourceUnknown, 1); err == nil || errors.Is(err, ErrNoOwner) {
		t.Errorf("ServiceOf(unknown kind) error = %v, want a failure that is not ErrNoOwner", err)
	}
}
