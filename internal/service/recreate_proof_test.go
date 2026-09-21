package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// TestRecreateProofAdminFE drives the seeded admin-fe service end to end
// through the generic engine's public API: deploy, then rollback, then
// history. It is the S6.4 second-strategy proof: deploy and rollback reach
// the shared execute path with no tn-api-specific branches, and the only
// setup is editing the service's data row (ADR-0006), exactly as an
// operator would on the host.
func TestRecreateProofAdminFE(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	script := writeTempFile(t, "#!/bin/sh\nexit 0\n", 0o755)

	runner := func(
		_ context.Context, _, _ string, args []string, emit func(string),
	) error {
		if args[0] != testServiceFlag || args[1] != testAdminService {
			return errors.New("wrong service args")
		}

		emit("[1/2] recreate")
		emit("[2/2] done")

		return nil
	}

	containers := []docker.Container{
		{
			ID: "app-id", Name: "ticketnation-admin-app-1",
			Image: "ticketnation-admin:latest", ImageID: "sha256:appbeef",
			State: testRunningState,
			Labels: map[string]string{
				docker.LabelComposeProject: "ticketnation-admin",
				docker.LabelComposeService: testAdminService,
			},
		},
	}

	prober := stubProber{healthy: map[string]bool{testAdminHealthURL: true}}
	cutover, db, log := testCutover(t, runner, containers, prober)

	configureRecreateScripts(t, db, testServiceAdminFE, script)

	target, err := cutover.StartDeploy(ctx, testServiceAdminFE, testActor)
	if err != nil {
		t.Fatalf("StartDeploy() error = %v, want nil", err)
	}

	if target != testAdminService {
		t.Errorf("StartDeploy() target = %q, want app", target)
	}

	waitForEvents(t, log, 3)
	proofCheckDeployed(t, db, testServiceAdminFE)

	target = startRollbackSoon(t, cutover, testServiceAdminFE)
	if target != testAdminService {
		t.Errorf("StartRollback() target = %q, want app", target)
	}

	waitForEvents(t, log, 6)
	proofCheckRolledBack(t, db, testServiceAdminFE)
	proofCheckHistoryLen(t, db, testServiceAdminFE, 2)
	proofCheckNotifications(t, db, 2)
	proofCheckAudit(t, db, testServiceAdminFE)
}

// proofCheckDeployed validates the recorded deploy success.
func proofCheckDeployed(t *testing.T, db *store.DB, serviceID string) {
	t.Helper()

	deployed := proofLatestDeploy(t, db, serviceID)
	if deployed.Type != model.DeployDeploy || deployed.Outcome != model.DeploySuccess {
		t.Fatalf("deploy = %+v, want deploy success", deployed)
	}

	if deployed.SHA != "sha256:appbeef" {
		t.Errorf("deploy sha = %q, want live image id", deployed.SHA)
	}

	if deployed.DowntimeSecs == nil {
		t.Error("deploy downtime is nil, want probed")
	}
}

// proofCheckRolledBack validates the recorded rollback success.
func proofCheckRolledBack(t *testing.T, db *store.DB, serviceID string) {
	t.Helper()

	rolledBack := proofLatestDeploy(t, db, serviceID)
	if rolledBack.Type != model.DeployRollback || rolledBack.Outcome != model.DeploySuccess {
		t.Fatalf("deploy = %+v, want rollback success", rolledBack)
	}
}

// proofCheckHistoryLen validates the history entry count.
func proofCheckHistoryLen(t *testing.T, db *store.DB, serviceID string, want int) {
	t.Helper()

	history, err := store.NewDeployStore(db).ListByService(context.Background(), serviceID, 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(history) != want {
		t.Fatalf("history = %d entries, want %d (deploy + rollback)", len(history), want)
	}
}

// proofCheckNotifications validates the deploy notification count.
func proofCheckNotifications(t *testing.T, db *store.DB, want int) {
	t.Helper()

	notifications, err := store.NewNotificationStore(db).List(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(notifications) != want {
		t.Errorf("notifications = %d, want %d (deploy + rollback)", len(notifications), want)
	}
}

// proofCheckAudit validates the deploy + rollback audit entries.
func proofCheckAudit(t *testing.T, db *store.DB, serviceID string) {
	t.Helper()

	entries, err := store.NewAuditStore(db).List(context.Background(), serviceID, 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	actions := map[string]bool{}
	for _, entry := range entries {
		actions[entry.Action] = true

		if entry.Result != model.AuditSuccess {
			t.Errorf("audit %s result = %q, want success", entry.Action, entry.Result)
		}
	}

	if !actions[model.AuditDeploy] || !actions[model.AuditRollback] {
		t.Errorf("audit actions = %v, want deploy + rollback", actions)
	}
}

// configureRecreateScripts edits a seeded recreate row's data config,
// the operator path from ADR-0006.
func configureRecreateScripts(t *testing.T, db *store.DB, serviceID, script string) {
	t.Helper()

	ctx := context.Background()
	services := store.NewServiceStore(db)

	def, err := services.Get(ctx, serviceID)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	var cfg model.RecreateConfig

	if err := json.Unmarshal(def.Config, &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v, want nil", err)
	}

	cfg.DeployScript = script
	cfg.RollbackScript = script

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal() error = %v, want nil", err)
	}

	if err := services.UpdateConfig(ctx, serviceID, raw); err != nil {
		t.Fatalf("UpdateConfig() error = %v, want nil", err)
	}
}

// proofLatestDeploy returns the newest history entry for a service.
func proofLatestDeploy(t *testing.T, db *store.DB, serviceID string) model.Deploy {
	t.Helper()

	deploys, err := store.NewDeployStore(db).ListByService(context.Background(), serviceID, 1)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(deploys) != 1 {
		t.Fatalf("history = %d entries, want at least 1", len(deploys))
	}

	return deploys[0]
}

// startRollbackSoon retries StartRollback past the deploy's run release,
// which lands just after its finished event.
func startRollbackSoon(t *testing.T, cutover *Cutover, serviceID string) string {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		target, err := cutover.StartRollback(context.Background(), serviceID, testActor)
		if err == nil {
			return target
		}

		if !errors.Is(err, ErrConflict) {
			t.Fatalf("StartRollback() error = %v, want nil", err)
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("StartRollback() never cleared the deploy conflict")

	return ""
}
