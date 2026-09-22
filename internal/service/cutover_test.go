package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/probe"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// eventLog collects emitted events across goroutines.
type eventLog struct {
	mutex  sync.Mutex
	events []model.Event
}

func (l *eventLog) emit(event model.Event) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	l.events = append(l.events, event)
}

func (l *eventLog) all() []model.Event {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	return append([]model.Event{}, l.events...)
}

// testCutover builds a Cutover with fakes on the seeded store.
func testCutover(
	t *testing.T,
	runner Runner,
	containers []docker.Container,
	prober Prober,
) (*Cutover, *store.DB, *eventLog) {
	t.Helper()

	db := openInventoryDB(t)
	log := &eventLog{}
	services := store.NewServiceStore(db)

	cutover := NewCutover(CutoverConfig{
		Services:      services,
		Docker:        stubLister{containers: containers},
		Deploys:       NewDeploys(services, store.NewDeployStore(db)),
		Probes:        store.NewDeployStore(db),
		Notifications: store.NewNotificationStore(db),
		Audit:         NewAudit(services, store.NewAuditStore(db)),
		Prober:        prober,
		Emit:          log.emit,
		Run:           runner,
		Timeout:       time.Minute,
		ProbeInterval: time.Millisecond,
		Logger:        slog.New(slog.DiscardHandler),
	})

	return cutover, db, log
}

// testBlueGreenDef builds a blue-green definition with the given script and conf.
func testBlueGreenDef(t *testing.T, script, conf string) (model.Service, model.BlueGreenConfig) {
	t.Helper()

	cfg := model.BlueGreenConfig{
		BlueService: testBlueService, GreenService: testGreenService,
		BlueTarget: "127.0.0.1:4101", GreenTarget: "127.0.0.1:4102", LegacyTarget: "127.0.0.1:4000",
		NginxConf: conf, Marker: "# BLUEGREEN-ACTIVE",
		BlueURL: testBlueHealthURL, GreenURL: "http://127.0.0.1:4102/health",
		PublicURL:     "https://api.example.test/health",
		CutoverScript: script,
	}

	def := model.Service{
		ID: "test-api", Name: "test-api", Strategy: model.StrategyBlueGreen,
		ComposeProject: testComposeProject, ComposeDir: t.TempDir(), Config: marshalConfig(t, cfg),
	}

	return def, cfg
}

// testRecreateDef builds a recreate definition with the given scripts.
func testRecreateDef(t *testing.T, deployScript, rollbackScript string) (model.Service, model.RecreateConfig) {
	t.Helper()

	cfg := model.RecreateConfig{
		Service: testRecreateService, HealthURL: testRecreateHealthURL,
		PublicURL:    "https://fe.example.test/health",
		DeployScript: deployScript, RollbackScript: rollbackScript,
	}

	def := model.Service{
		ID: "test-fe", Name: "test-fe", Strategy: model.StrategyRecreate,
		ComposeProject: testComposeProject, ComposeDir: t.TempDir(), Config: marshalConfig(t, cfg),
	}

	return def, cfg
}

// marshalConfig marshals strategy config or fails the test.
func marshalConfig(t *testing.T, cfg any) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshaling config: %v", err)
	}

	return raw
}

// writeTempFile writes content to a temp file with the given mode.
func writeTempFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cutover-test")

	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}

	return path
}

const nginxBlueConf = `upstream tn_api_active {
    server 127.0.0.1:4101; # BLUEGREEN-ACTIVE
}
`

const nginxLegacyConf = `upstream tn_api_active {
    server 127.0.0.1:4000; # BLUEGREEN-ACTIVE
}
`

const nginxGreenConf = `upstream tn_api_active {
    server 127.0.0.1:4102; # BLUEGREEN-ACTIVE
}
`

func TestStartValidation(t *testing.T) {
	t.Parallel()

	cutover, _, _ := testCutover(t, nil, nil, stubProber{})
	ctx := context.Background()

	tests := []struct {
		name      string
		serviceID string
		target    string
	}{
		{name: testUnknownServiceName, serviceID: testUnknownServiceID, target: "auto"},
		{name: "recreate service", serviceID: testServiceFE, target: "auto"},
		{name: "bad target", serviceID: testServiceAPI, target: "purple"},
		{name: "no script configured", serviceID: testServiceAPI, target: colorGreen},
		{name: "auto unresolvable", serviceID: testServiceAPI, target: "auto"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := cutover.Start(ctx, tt.serviceID, tt.target, testActor); err == nil {
				t.Errorf("Start(%q, %q) error = nil, want error", tt.serviceID, tt.target)
			}
		})
	}
}

func TestStartRollbackValidation(t *testing.T) {
	t.Parallel()

	cutover, _, _ := testCutover(t, nil, nil, stubProber{})
	ctx := context.Background()

	checkStartCases(t, "StartRollback", []startCase{
		{name: testUnknownServiceName, serviceID: testUnknownServiceID},
		{name: "recreate service", serviceID: testServiceFE},
		{name: "no script configured", serviceID: testServiceAPI},
	}, func(serviceID string) error {
		_, err := cutover.StartRollback(ctx, serviceID, testActor)

		return err
	})
}

func TestStartDeployValidation(t *testing.T) {
	t.Parallel()

	cutover, _, _ := testCutover(t, nil, nil, stubProber{})
	ctx := context.Background()

	checkStartCases(t, "StartDeploy", []startCase{
		{name: testUnknownServiceName, serviceID: testUnknownServiceID},
		{name: "blue-green service", serviceID: testServiceAPI},
		{name: "no script configured", serviceID: testServiceFE},
	}, func(serviceID string) error {
		_, err := cutover.StartDeploy(ctx, serviceID, testActor)

		return err
	})
}

// startCase is one start-validation case.
type startCase struct {
	name      string
	serviceID string
}

// checkStartCases fails when any case starts without error.
func checkStartCases(
	t *testing.T,
	noun string,
	cases []startCase,
	start func(serviceID string) error,
) {
	t.Helper()

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := start(tt.serviceID); err == nil {
				t.Errorf("%s(%q) error = nil, want error", noun, tt.serviceID)
			}
		})
	}
}

func TestBeginResolvesAuto(t *testing.T) {
	t.Parallel()

	script := writeTempFile(t, "#!/bin/sh\nexit 0\n", 0o755)
	conf := writeTempFile(t, nginxBlueConf, 0o644)

	cutover, _, log := testCutover(t, func(
		_ context.Context, _, _ string, _ []string, _ func(string),
	) error {
		return nil
	}, nil, stubProber{})

	def, cfg := testBlueGreenDef(t, script, conf)

	target, err := cutover.begin(context.Background(), def, cfg, "auto", testActor)
	if err != nil {
		t.Fatalf("begin() error = %v, want nil", err)
	}

	if target != colorGreen {
		t.Errorf("begin() target = %q, want green (blue is live)", target)
	}

	// The guard is set synchronously, so the second begin conflicts even
	// though the first run is still in flight.
	if _, err := cutover.begin(context.Background(), def, cfg, colorBlue, testActor); !errors.Is(err, ErrConflict) {
		t.Errorf("second begin() error = %v, want ErrConflict", err)
	}

	waitForEvents(t, log, 2)
}

// countingProber tracks health-check calls during rollback tests.
type countingProber struct {
	mutex  sync.Mutex
	calls  int
	health map[string]bool
}

// Check records the call and reports the configured health.
func (s *countingProber) Check(_ context.Context, target string) (probe.Result, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.calls++

	if s.health[target] {
		return probe.Result{Healthy: true, StatusCode: 200}, nil
	}

	return probe.Result{Healthy: false, StatusCode: 500}, nil
}

// checkExecuteEvents validates started/progress/finished event order.
func checkExecuteEvents(t *testing.T, log *eventLog) {
	t.Helper()

	events := log.all()
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4 (started, 2 progress, finished)", len(events))
	}

	if events[0].Type != model.EventDeployStarted || events[3].Type != model.EventDeployFinished {
		t.Errorf("event types = %q..%q, want started..finished", events[0].Type, events[3].Type)
	}
}

// checkRollbackEvents validates rollback started/progress/finished order.
func checkRollbackEvents(t *testing.T, log *eventLog) {
	t.Helper()

	events := log.all()
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4 (started, 2 progress, finished)", len(events))
	}

	if events[0].Type != model.EventRollbackStarted || events[3].Type != model.EventRollbackFinished {
		t.Errorf("event types = %q..%q, want started..finished", events[0].Type, events[3].Type)
	}
}

// successfulDeploy returns the one recorded deploy entry.
func successfulDeploy(t *testing.T, db *store.DB, serviceID string) model.Deploy {
	t.Helper()

	deploys, err := store.NewDeployStore(db).ListByService(context.Background(), serviceID, 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(deploys) != 1 {
		t.Fatalf("deploys = %d, want 1", len(deploys))
	}

	return deploys[0]
}

// checkDeployAudit validates the one deploy audit entry.
func checkDeployAudit(t *testing.T, db *store.DB, serviceID, action, result string) {
	t.Helper()

	entries, err := store.NewAuditStore(db).List(context.Background(), serviceID, 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(entries) != 1 || entries[0].Action != action || entries[0].Result != result {
		t.Fatalf("audit = %+v, want one %s %s entry", entries, action, result)
	}
}

// checkDeployNotification validates the one deploy notification.
func checkDeployNotification(t *testing.T, db *store.DB) {
	t.Helper()

	notifications, err := store.NewNotificationStore(db).List(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(notifications) != 1 || notifications[0].Kind != model.NotificationDeploy {
		t.Errorf("notifications = %+v, want one deploy note", notifications)
	}
}

// waitForEvents polls until the log holds n events or the timeout lapses.
func waitForEvents(t *testing.T, log *eventLog, n int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		if len(log.all()) >= n {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("events = %d, want at least %d", len(log.all()), n)
}

func TestBeginRejects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("missing script", func(t *testing.T) {
		t.Parallel()

		cutover, _, _ := testCutover(t, nil, nil, stubProber{})
		def, cfg := testBlueGreenDef(t, "/nonexistent-cutover.sh", "/nonexistent.conf")

		if _, err := cutover.begin(ctx, def, cfg, colorGreen, testActor); err == nil {
			t.Error("begin() error = nil, want missing script error")
		}
	})

	t.Run("non-executable script", func(t *testing.T) {
		t.Parallel()

		script := writeTempFile(t, "#!/bin/sh\nexit 0\n", 0o644)
		cutover, _, _ := testCutover(t, nil, nil, stubProber{})
		def, cfg := testBlueGreenDef(t, script, "/nonexistent.conf")

		if _, err := cutover.begin(ctx, def, cfg, colorGreen, testActor); err == nil {
			t.Error("begin() error = nil, want non-executable error")
		}
	})

	t.Run("auto with legacy live", func(t *testing.T) {
		t.Parallel()

		script := writeTempFile(t, "#!/bin/sh\nexit 0\n", 0o755)
		conf := writeTempFile(t, nginxLegacyConf, 0o644)
		cutover, _, _ := testCutover(t, nil, nil, stubProber{})
		def, cfg := testBlueGreenDef(t, script, conf)

		if _, err := cutover.begin(ctx, def, cfg, "auto", testActor); err == nil {
			t.Error("begin() error = nil, want unresolvable auto error")
		}
	})
}

func TestBeginRollbackResolvesOpposite(t *testing.T) {
	t.Parallel()

	script := writeTempFile(t, "#!/bin/sh\nexit 0\n", 0o755)
	conf := writeTempFile(t, nginxGreenConf, 0o644)

	cutover, _, log := testCutover(t, func(
		_ context.Context, _, _ string, _ []string, _ func(string),
	) error {
		return nil
	}, nil, stubProber{healthy: map[string]bool{testBlueHealthURL: true}})

	def, cfg := testBlueGreenDef(t, script, conf)

	target, err := cutover.beginRollback(context.Background(), def, cfg, testActor)
	if err != nil {
		t.Fatalf("beginRollback() error = %v, want nil", err)
	}

	if target != colorBlue {
		t.Errorf("beginRollback() target = %q, want blue (green is live)", target)
	}

	if _, err := cutover.begin(context.Background(), def, cfg, colorGreen, testActor); !errors.Is(err, ErrConflict) {
		t.Errorf("cutover during rollback error = %v, want ErrConflict", err)
	}

	waitForEvents(t, log, 2)
}

func TestBeginRollbackRejects(t *testing.T) {
	t.Parallel()

	script := writeTempFile(t, "#!/bin/sh\nexit 0\n", 0o755)
	legacy := writeTempFile(t, nginxLegacyConf, 0o644)

	tests := []struct {
		name   string
		script string
		conf   string
	}{
		{name: "missing script", script: "/nonexistent-rollback.sh", conf: "/nonexistent.conf"},
		{name: "legacy live", script: script, conf: legacy},
		{name: "unknown live", script: script, conf: "/nonexistent.conf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cutover, _, _ := testCutover(t, nil, nil, stubProber{})
			def, cfg := testBlueGreenDef(t, tt.script, tt.conf)

			if _, err := cutover.beginRollback(context.Background(), def, cfg, testActor); err == nil {
				t.Error("beginRollback() error = nil, want resolution error")
			}
		})
	}
}

func TestBeginDeployResolves(t *testing.T) {
	t.Parallel()

	script := writeTempFile(t, "#!/bin/sh\nexit 0\n", 0o755)

	cutover, _, log := testCutover(t, func(
		_ context.Context, _, _ string, _ []string, _ func(string),
	) error {
		return nil
	}, nil, stubProber{healthy: map[string]bool{testRecreateHealthURL: true}})

	def, cfg := testRecreateDef(t, script, script)

	target, err := cutover.beginDeploy(context.Background(), def, cfg, testActor)
	if err != nil {
		t.Fatalf("beginDeploy() error = %v, want nil", err)
	}

	if target != testRecreateService {
		t.Errorf("beginDeploy() target = %q, want %q", target, testRecreateService)
	}

	if _, err := cutover.beginDeploy(context.Background(), def, cfg, testActor); !errors.Is(err, ErrConflict) {
		t.Errorf("second beginDeploy() error = %v, want ErrConflict", err)
	}

	waitForEvents(t, log, 2)
}

func TestBeginDeployRejects(t *testing.T) {
	t.Parallel()

	script := writeTempFile(t, "#!/bin/sh\nexit 0\n", 0o755)
	plain := writeTempFile(t, "#!/bin/sh\nexit 0\n", 0o644)
	ctx := context.Background()

	t.Run("missing script", func(t *testing.T) {
		t.Parallel()

		cutover, _, _ := testCutover(t, nil, nil, stubProber{})
		def, cfg := testRecreateDef(t, "/nonexistent-deploy.sh", script)

		if _, err := cutover.beginDeploy(ctx, def, cfg, testActor); err == nil {
			t.Error("beginDeploy() error = nil, want missing script error")
		}
	})

	t.Run("non-executable script", func(t *testing.T) {
		t.Parallel()

		cutover, _, _ := testCutover(t, nil, nil, stubProber{})
		def, cfg := testRecreateDef(t, plain, script)

		if _, err := cutover.beginDeploy(ctx, def, cfg, testActor); err == nil {
			t.Error("beginDeploy() error = nil, want non-executable error")
		}
	})

	t.Run("missing service", func(t *testing.T) {
		t.Parallel()

		cutover, _, _ := testCutover(t, nil, nil, stubProber{})
		def, cfg := testRecreateDef(t, script, script)
		cfg.Service = ""

		if _, err := cutover.beginDeploy(ctx, def, cfg, testActor); err == nil {
			t.Error("beginDeploy() error = nil, want missing service error")
		}
	})

	t.Run("missing health url", func(t *testing.T) {
		t.Parallel()

		cutover, _, _ := testCutover(t, nil, nil, stubProber{})
		def, cfg := testRecreateDef(t, script, script)
		cfg.HealthURL = ""

		if _, err := cutover.beginDeploy(ctx, def, cfg, testActor); err == nil {
			t.Error("beginDeploy() error = nil, want missing health url error")
		}
	})
}

func TestBeginRecreateRollbackResolves(t *testing.T) {
	t.Parallel()

	script := writeTempFile(t, "#!/bin/sh\nexit 0\n", 0o755)

	cutover, _, log := testCutover(t, func(
		_ context.Context, _, _ string, _ []string, _ func(string),
	) error {
		return nil
	}, nil, stubProber{healthy: map[string]bool{testRecreateHealthURL: true}})

	def, cfg := testRecreateDef(t, script, script)

	target, err := cutover.beginRecreateRollback(context.Background(), def, cfg, testActor)
	if err != nil {
		t.Fatalf("beginRecreateRollback() error = %v, want nil", err)
	}

	if target != testRecreateService {
		t.Errorf("beginRecreateRollback() target = %q, want %q", target, testRecreateService)
	}

	waitForEvents(t, log, 2)
}

func TestBeginRecreateRollbackRejects(t *testing.T) {
	t.Parallel()

	script := writeTempFile(t, "#!/bin/sh\nexit 0\n", 0o755)
	ctx := context.Background()

	t.Run("missing script", func(t *testing.T) {
		t.Parallel()

		cutover, _, _ := testCutover(t, nil, nil, stubProber{})
		def, cfg := testRecreateDef(t, script, "/nonexistent-rollback.sh")

		if _, err := cutover.beginRecreateRollback(ctx, def, cfg, testActor); err == nil {
			t.Error("beginRecreateRollback() error = nil, want missing script error")
		}
	})

	t.Run("unconfigured", func(t *testing.T) {
		t.Parallel()

		cutover, _, _ := testCutover(t, nil, nil, stubProber{})
		def, cfg := testRecreateDef(t, script, "")

		if _, err := cutover.beginRecreateRollback(ctx, def, cfg, testActor); err == nil {
			t.Error("beginRecreateRollback() error = nil, want unconfigured error")
		}
	})
}

func TestActive(t *testing.T) {
	t.Parallel()

	cutover, _, _ := testCutover(t, nil, nil, stubProber{})

	if cutover.Active(testServiceAPI) {
		t.Fatal("Active() = true, want false before claim")
	}

	if err := cutover.claimRun(testServiceAPI); err != nil {
		t.Fatalf("claimRun() error = %v, want nil", err)
	}

	if !cutover.Active(testServiceAPI) {
		t.Error("Active() = false, want true while claimed")
	}

	if cutover.Active("other") {
		t.Error("Active(other) = true, want false")
	}

	cutover.releaseRun(testServiceAPI)

	if cutover.Active(testServiceAPI) {
		t.Error("Active() = true, want false after release")
	}
}

func TestExecuteSuccess(t *testing.T) {
	t.Parallel()

	containers := []docker.Container{
		{
			ID: "green-id", Name: "ticketnation-api-green-1",
			Image: "ticketnation-api:latest", ImageID: "sha256:deadbeef",
			State: testRunningState,
			Labels: map[string]string{
				docker.LabelComposeProject: testComposeProject,
				docker.LabelComposeService: testGreenService,
			},
		},
	}

	runner := func(
		_ context.Context, _, _ string, args []string, emit func(string),
	) error {
		if args[0] != testTargetFlag || args[1] != colorGreen {
			return errors.New("wrong target args")
		}

		emit("[1/5] preflight")
		emit("[5/5] done")

		return nil
	}

	cutover, db, log := testCutover(t, runner, containers, stubProber{})
	def, _ := testBlueGreenDef(t, testTrueBinary, "/nonexistent.conf")
	def.ID = testServiceAPI

	cutover.execute(context.Background(), operation{
		def: def, script: testTrueBinary, args: []string{testTargetFlag, colorGreen},
		shaService: testGreenService,
		target:     colorGreen, actor: testActor, deployType: model.DeployCutover,
	})
	checkExecuteEvents(t, log)

	got := successfulDeploy(t, db, def.ID)
	if got.Outcome != model.DeploySuccess || got.Type != model.DeployCutover || got.Actor != testActor {
		t.Errorf("deploy = %+v, want recorded success", got)
	}

	if got.SHA != "sha256:deadbeef" {
		t.Errorf("deploy sha = %q, want live image id", got.SHA)
	}

	if got.DurationSecs == nil {
		t.Error("deploy duration is nil, want computed")
	}

	if got.DowntimeSecs != nil {
		t.Errorf("deploy downtime = %v, want nil without public url", *got.DowntimeSecs)
	}

	checkDeployNotification(t, db)
	checkDeployAudit(t, db, def.ID, model.AuditCutover, model.AuditSuccess)
}

func TestExecuteFailure(t *testing.T) {
	t.Parallel()

	runner := func(
		_ context.Context, _, _ string, _ []string, emit func(string),
	) error {
		emit("[1/5] preflight")

		return errors.New("health check failed")
	}

	cutover, db, log := testCutover(t, runner, nil, stubProber{})
	def, _ := testBlueGreenDef(t, testTrueBinary, "/nonexistent.conf")
	def.ID = testServiceAPI

	cutover.execute(context.Background(), operation{
		def: def, script: testTrueBinary, args: []string{testTargetFlag, colorGreen},
		shaService: testGreenService,
		target:     colorGreen, actor: testActor, deployType: model.DeployCutover,
	})

	events := log.all()
	if len(events) != 3 || events[2].Type != model.EventDeployFinished {
		t.Fatalf("events = %+v, want started/progress/finished", events)
	}

	deploys, err := store.NewDeployStore(db).ListByService(context.Background(), def.ID, 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(deploys) != 1 || deploys[0].Outcome != model.DeployFailure {
		t.Fatalf("deploys = %+v, want one failure", deploys)
	}

	if deploys[0].Notes == "" || deploys[0].SHA != "unknown" {
		t.Errorf("deploy = %+v, want notes and unknown sha", deploys[0])
	}

	checkDeployAudit(t, db, def.ID, model.AuditCutover, model.AuditFailure)
}

func TestExecuteRollbackSuccess(t *testing.T) {
	t.Parallel()

	containers := []docker.Container{
		{
			ID: testBlueContainerID, Name: testBlueContainerName,
			Image: "ticketnation-api:latest", ImageID: "sha256:bluebeef",
			State: testRunningState,
			Labels: map[string]string{
				docker.LabelComposeProject: testComposeProject,
				docker.LabelComposeService: testBlueService,
			},
		},
	}

	runner := func(
		_ context.Context, _, _ string, args []string, emit func(string),
	) error {
		if args[0] != testTargetFlag || args[1] != colorBlue {
			return errors.New("wrong target args")
		}

		emit("[1/5] preflight")
		emit("[5/5] done")

		return nil
	}
	prober := &countingProber{health: map[string]bool{testBlueHealthURL: true}}

	cutover, db, log := testCutover(t, runner, containers, prober)
	def, _ := testBlueGreenDef(t, testTrueBinary, "/nonexistent.conf")
	def.ID = testServiceAPI

	cutover.execute(context.Background(), operation{
		def: def, script: testTrueBinary, args: []string{testTargetFlag, colorBlue},
		shaService: testBlueService, healthURL: testBlueHealthURL,
		target: colorBlue, actor: testActor,
		deployType: model.DeployRollback, verifyHealth: true,
	})
	checkRollbackEvents(t, log)

	got := successfulDeploy(t, db, def.ID)
	if got.Outcome != model.DeploySuccess || got.Type != model.DeployRollback || got.Actor != testActor {
		t.Errorf("deploy = %+v, want recorded rollback success", got)
	}

	if got.SHA != "sha256:bluebeef" {
		t.Errorf("deploy sha = %q, want live image id", got.SHA)
	}

	if got.DurationSecs == nil {
		t.Error("deploy duration is nil, want computed")
	}

	if prober.calls != 1 {
		t.Errorf("health checks = %d, want 1", prober.calls)
	}

	checkDeployNotification(t, db)
	checkDeployAudit(t, db, def.ID, model.AuditRollback, model.AuditSuccess)
}

func TestExecuteRollbackUnhealthy(t *testing.T) {
	t.Parallel()

	runner := func(
		_ context.Context, _, _ string, _ []string, _ func(string),
	) error {
		return nil
	}
	prober := &countingProber{health: map[string]bool{}}

	cutover, db, log := testCutover(t, runner, nil, prober)
	def, _ := testBlueGreenDef(t, testTrueBinary, "/nonexistent.conf")
	def.ID = testServiceAPI

	cutover.execute(context.Background(), operation{
		def: def, script: testTrueBinary, args: []string{testTargetFlag, colorBlue},
		shaService: testBlueService, healthURL: testBlueHealthURL,
		target: colorBlue, actor: testActor,
		deployType: model.DeployRollback, verifyHealth: true,
	})

	events := log.all()
	if len(events) != 2 || events[1].Type != model.EventRollbackFinished {
		t.Fatalf("events = %+v, want started/finished", events)
	}

	got := successfulDeploy(t, db, def.ID)
	if got.Outcome != model.DeployFailure || got.Type != model.DeployRollback {
		t.Fatalf("deploy = %+v, want rollback failure", got)
	}

	want := "rollback target blue failed post-rollback health check"
	if got.Notes != want {
		t.Errorf("notes = %q, want %q", got.Notes, want)
	}

	if prober.calls != 1 {
		t.Errorf("health checks = %d, want 1", prober.calls)
	}

	checkDeployAudit(t, db, def.ID, model.AuditRollback, model.AuditFailure)
}

func TestExecuteRollbackScriptFailure(t *testing.T) {
	t.Parallel()

	runner := func(
		_ context.Context, _, _ string, _ []string, emit func(string),
	) error {
		emit("[1/5] preflight")

		return errors.New("idle color never became healthy")
	}
	prober := &countingProber{health: map[string]bool{testBlueHealthURL: true}}

	cutover, db, log := testCutover(t, runner, nil, prober)
	def, _ := testBlueGreenDef(t, testTrueBinary, "/nonexistent.conf")
	def.ID = testServiceAPI

	cutover.execute(context.Background(), operation{
		def: def, script: testTrueBinary, args: []string{testTargetFlag, colorBlue},
		shaService: testBlueService, healthURL: testBlueHealthURL,
		target: colorBlue, actor: testActor,
		deployType: model.DeployRollback, verifyHealth: true,
	})

	events := log.all()
	if len(events) != 3 || events[2].Type != model.EventRollbackFinished {
		t.Fatalf("events = %+v, want started/progress/finished", events)
	}

	got := successfulDeploy(t, db, def.ID)
	if got.Outcome != model.DeployFailure || got.Notes == "" {
		t.Fatalf("deploy = %+v, want failure with notes", got)
	}

	if prober.calls != 0 {
		t.Errorf("health checks = %d, want 0 after script failure", prober.calls)
	}

	checkDeployAudit(t, db, def.ID, model.AuditRollback, model.AuditFailure)
}

func TestExecuteRecreateSuccess(t *testing.T) {
	t.Parallel()

	containers := []docker.Container{
		{
			ID: "fe-id", Name: "ticketnation-fe-1",
			Image: "ticketnation-fe:latest", ImageID: "sha256:febeef",
			State: testRunningState,
			Labels: map[string]string{
				docker.LabelComposeProject: testComposeProject,
				docker.LabelComposeService: testRecreateService,
			},
		},
	}

	runner := func(
		_ context.Context, _, _ string, args []string, emit func(string),
	) error {
		if args[0] != testServiceFlag || args[1] != testRecreateService {
			return errors.New("wrong service args")
		}

		emit("[1/2] recreate")
		emit("[2/2] done")

		return nil
	}
	prober := &countingProber{health: map[string]bool{testRecreateHealthURL: true}}

	cutover, db, log := testCutover(t, runner, containers, prober)
	def, cfg := testRecreateDef(t, testTrueBinary, testTrueBinary)
	def.ID = testServiceFE

	cutover.execute(context.Background(), operation{
		def: def, script: testTrueBinary, args: []string{testServiceFlag, testRecreateService},
		shaService: testRecreateService, healthURL: testRecreateHealthURL, publicURL: cfg.PublicURL,
		target: testRecreateService, actor: testActor,
		deployType: model.DeployDeploy, verifyHealth: true,
	})
	checkExecuteEvents(t, log)

	got := successfulDeploy(t, db, def.ID)
	if got.Outcome != model.DeploySuccess || got.Type != model.DeployDeploy || got.Actor != testActor {
		t.Errorf("deploy = %+v, want recorded deploy success", got)
	}

	if got.SHA != "sha256:febeef" {
		t.Errorf("deploy sha = %q, want live image id", got.SHA)
	}

	if got.DurationSecs == nil {
		t.Error("deploy duration is nil, want computed")
	}

	if got.DowntimeSecs == nil {
		t.Error("deploy downtime is nil, want probed")
	}

	// The health verify plus any public-url samples all hit the prober.
	if prober.calls < 1 {
		t.Errorf("health checks = %d, want at least 1", prober.calls)
	}

	checkDeployNotification(t, db)
	checkDeployAudit(t, db, def.ID, model.AuditDeploy, model.AuditSuccess)
}

func TestSampleProbeRecords(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	url := "https://fe.example.test/health"
	prober := &countingProber{health: map[string]bool{}}

	cutover, db, _ := testCutover(t, nil, nil, prober)

	var failed atomic.Int64

	since := time.Now().Add(-time.Minute)
	cutover.sampleProbe(ctx, testServiceFE, url, time.Now(), &failed)

	if failed.Load() != 1 {
		t.Errorf("failed = %d, want 1", failed.Load())
	}

	prober.mutex.Lock()
	prober.health[url] = true
	prober.mutex.Unlock()

	cutover.sampleProbe(ctx, testServiceFE, url, time.Now(), &failed)

	if failed.Load() != 1 {
		t.Errorf("failed = %d, want 1 after healthy sample", failed.Load())
	}

	count, err := store.NewDeployStore(db).FailedProbeCount(ctx, testServiceFE, since, time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("FailedProbeCount() error = %v, want nil", err)
	}

	if count != 1 {
		t.Errorf("failed samples = %d, want 1", count)
	}
}

func TestCutoverArgs(t *testing.T) {
	t.Parallel()

	def := model.Service{ID: "test-api", ComposeDir: testComposeDir}
	full := model.BlueGreenConfig{
		BlueTarget: "b", GreenTarget: "g", LegacyTarget: "l", NginxConf: "/etc/nginx/test",
		BlueURL: "bu", GreenURL: "gu", PublicURL: "pu",
		ComposeFile: "docker-compose.yml", Project: "test", EnvFile: ".env",
		Sudo: true, SettleSecs: 5, HealthTimeout: 60, PublicTimeout: 30,
	}

	got := cutoverArgs(def, full, colorGreen)
	want := []string{
		testTargetFlag, colorGreen, "--compose-dir", testComposeDir, "--nginx-conf", "/etc/nginx/test",
		"--blue-server", "b", "--green-server", "g", "--legacy-server", "l",
		"--blue-url", "bu", "--green-url", "gu", "--public-url", "pu",
		"--compose-file", "docker-compose.yml", "--project", "test", "--env-file", ".env",
		"--sudo", "--settle-seconds", "5", "--health-timeout", "60", "--public-timeout", "30",
	}

	if !slices.Equal(got, want) {
		t.Errorf("cutoverArgs() =\n%v\nwant\n%v", got, want)
	}

	minimal := model.BlueGreenConfig{BlueTarget: "b"}
	got = cutoverArgs(def, minimal, colorBlue)

	if !slices.Contains(got, "--no-sudo") {
		t.Errorf("minimal args = %v, want explicit --no-sudo", got)
	}

	if slices.Contains(got, "--compose-file") {
		t.Errorf("minimal args = %v, want no compose-file flag", got)
	}
}

func TestRecreateArgs(t *testing.T) {
	t.Parallel()

	def := model.Service{
		ID: "test-fe", ComposeProject: testComposeProject, ComposeDir: testComposeDir,
	}
	cfg := model.RecreateConfig{Service: testRecreateService}

	got := recreateArgs(def, cfg)
	want := []string{
		testServiceFlag, testRecreateService,
		"--project", testComposeProject,
		"--compose-dir", testComposeDir,
	}

	if !slices.Equal(got, want) {
		t.Errorf("recreateArgs() = %v, want %v", got, want)
	}
}

func TestRunScript(t *testing.T) {
	t.Parallel()

	t.Run("echo streams lines", func(t *testing.T) {
		t.Parallel()

		var lines []string

		err := runScript(context.Background(), t.TempDir(), "/bin/echo", []string{"hello"}, func(line string) {
			lines = append(lines, line)
		})
		if err != nil {
			t.Fatalf("runScript() error = %v, want nil", err)
		}

		if !slices.Equal(lines, []string{"hello"}) {
			t.Errorf("lines = %v, want [hello]", lines)
		}
	})

	t.Run("stderr merges and failure errors", func(t *testing.T) {
		t.Parallel()

		var lines []string

		err := runScript(context.Background(), t.TempDir(), "/bin/ls", []string{"/nonexistent-dir-xyz"}, func(line string) {
			lines = append(lines, line)
		})
		if err == nil {
			t.Fatal("runScript() error = nil, want failure")
		}

		if len(lines) == 0 {
			t.Error("lines empty, want merged stderr")
		}
	})

	t.Run("context timeout kills", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		err := runScript(ctx, t.TempDir(), "/bin/sleep", []string{"30"}, func(string) {})
		if err == nil {
			t.Fatal("runScript() error = nil, want timeout kill")
		}
	})
}
