package service

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// testOperator is the non-admin whose actions the audit rows must name.
const testOperator = "ops-dev"

// operatorFixture is a sampler, ingestor and deploy service on a
// database holding only the generic services.
type operatorFixture struct {
	db       *store.DB
	sampler  *Sampler
	ingestor *Ingestor
	deploys  *Deploys
}

// newOperatorFixture wires the services whose mutations a developer may
// call, on the generic two-service fixture.
func newOperatorFixture(t *testing.T) operatorFixture {
	t.Helper()

	_, db := openScopedUsers(t)
	services := store.NewServiceStore(db)
	audit := NewAudit(services, store.NewAuditStore(db))
	logger := slog.New(slog.DiscardHandler)

	return operatorFixture{
		db: db,
		sampler: NewSampler(SamplerConfig{
			Services: services, Docker: stubStatsLister{}, Metrics: store.NewMetricStore(db),
			Rules: store.NewRuleStore(db), Notifications: store.NewNotificationStore(db),
			Deploys: store.NewDeployStore(db), Occurrences: store.NewOccurrenceStore(db),
			Issues: store.NewIssueStore(db), IssueRules: store.NewIssueRuleStore(db),
			Logs: store.NewLogStore(db), SDKLogs: store.NewSDKLogStore(db), Fleet: store.NewFleetStore(db),
			Audit: audit, Interval: time.Hour, Logger: logger,
		}),
		ingestor: NewIngestor(IngestorConfig{
			Services: services, Keys: store.NewKeyStore(db), Occurrences: store.NewOccurrenceStore(db),
			Issues: store.NewIssueStore(db), IssueRules: store.NewIssueRuleStore(db),
			Logs: store.NewLogStore(db), Audit: audit, MaxOccurrences: 100, Logger: logger,
		}),
		deploys: NewDeploys(services, store.NewDeployStore(db)).WithAudit(audit),
	}
}

// auditRow is what a test compares of one audit entry.
type auditRow struct {
	service string
	actor   string
	action  string
	detail  string
}

// auditRows returns every audit entry oldest first.
func auditRows(t *testing.T, db *store.DB) []auditRow {
	t.Helper()

	entries, err := store.NewAuditStore(db).List(context.Background(), "", 100)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	rows := make([]auditRow, 0, len(entries))

	for _, entry := range slices.Backward(entries) {
		service := ""
		if entry.ServiceID != nil {
			service = *entry.ServiceID
		}

		if entry.Result != model.AuditSuccess {
			t.Errorf("audit entry %+v has result %s, want success", entry, entry.Result)
		}

		rows = append(rows, auditRow{service: service, actor: entry.Actor, action: entry.Action, detail: entry.Detail})
	}

	return rows
}

// wantAuditRows fails unless the audit trail is exactly want.
func wantAuditRows(t *testing.T, db *store.DB, want []auditRow) {
	t.Helper()

	got := auditRows(t, db)
	if len(got) != len(want) {
		t.Fatalf("audit rows = %+v, want %+v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("audit row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestAlertRuleMutationsAreAudited(t *testing.T) {
	t.Parallel()

	fixture := newOperatorFixture(t)
	ctx := context.Background()
	granted := testfixture.GenericGranted

	rule, err := fixture.sampler.CreateRule(ctx, model.RuleCreate{
		ServiceID: granted, Metric: model.MetricCPU, Threshold: 90, DurationSecs: 120,
	}, testOperator)
	if err != nil {
		t.Fatalf("CreateRule() error = %v, want nil", err)
	}

	if err := fixture.sampler.DeleteRule(ctx, rule.ID, testOperator); err != nil {
		t.Fatalf("DeleteRule() error = %v, want nil", err)
	}

	described := strconv.FormatInt(rule.ID, 10) + ": cpu above 90 for 120s"

	wantAuditRows(t, fixture.db, []auditRow{
		{service: granted, actor: testOperator, action: model.AuditAlertRuleCreate, detail: "created alert rule " + described},
		{service: granted, actor: testOperator, action: model.AuditAlertRuleDelete, detail: "deleted alert rule " + described},
	})

	// Nothing is stored or audited without an actor, for a rejected
	// rule, or for an id that names no rule.
	if _, err := fixture.sampler.CreateRule(ctx, model.RuleCreate{
		ServiceID: granted, Metric: model.MetricCPU, Threshold: 90, DurationSecs: 120,
	}, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("CreateRule(no actor) error = %v, want ErrInvalidInput", err)
	}

	if _, err := fixture.sampler.CreateRule(ctx, model.RuleCreate{
		ServiceID: granted, Metric: "nope", Threshold: 90, DurationSecs: 120,
	}, testOperator); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("CreateRule(bad metric) error = %v, want ErrInvalidInput", err)
	}

	if err := fixture.sampler.DeleteRule(ctx, rule.ID, testOperator); !errors.Is(err, store.ErrRuleNotFound) {
		t.Errorf("DeleteRule(deleted) error = %v, want ErrRuleNotFound", err)
	}

	if err := fixture.sampler.DeleteRule(ctx, rule.ID, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("DeleteRule(no actor) error = %v, want ErrInvalidInput", err)
	}

	if rules, err := fixture.sampler.Rules(ctx, granted); err != nil || len(rules) != 0 {
		t.Errorf("Rules() = %+v, %v, want none left", rules, err)
	}

	if got := auditRows(t, fixture.db); len(got) != 2 {
		t.Errorf("audit rows after refused calls = %+v, want the same two", got)
	}
}

func TestIssueRuleMutationsAreAudited(t *testing.T) {
	t.Parallel()

	fixture := newOperatorFixture(t)
	ctx := context.Background()
	other := testfixture.GenericOther

	spike, err := fixture.ingestor.CreateIssueRule(ctx, model.IssueRuleCreate{
		ServiceID: other, Kind: model.IssueRuleSpike, Threshold: 5, WindowSecs: 300,
	}, testOperator)
	if err != nil {
		t.Fatalf("CreateIssueRule(spike) error = %v, want nil", err)
	}

	fresh, err := fixture.ingestor.CreateIssueRule(ctx, model.IssueRuleCreate{
		ServiceID: other, Kind: model.IssueRuleNewIssue, WindowSecs: 60,
	}, testOperator)
	if err != nil {
		t.Fatalf("CreateIssueRule(new issue) error = %v, want nil", err)
	}

	for _, id := range []int64{spike.ID, fresh.ID} {
		if err := fixture.ingestor.DeleteIssueRule(ctx, id, testOperator); err != nil {
			t.Fatalf("DeleteIssueRule(%d) error = %v, want nil", id, err)
		}
	}

	wantAuditRows(t, fixture.db, []auditRow{
		{service: other, actor: testOperator, action: model.AuditIssueRuleCreate, detail: "created issue rule 1: spike of 5 in 300s"},
		{service: other, actor: testOperator, action: model.AuditIssueRuleCreate, detail: "created issue rule 2: new_issue within 60s"},
		{service: other, actor: testOperator, action: model.AuditIssueRuleDelete, detail: "deleted issue rule 1: spike of 5 in 300s"},
		{service: other, actor: testOperator, action: model.AuditIssueRuleDelete, detail: "deleted issue rule 2: new_issue within 60s"},
	})

	if _, err := fixture.ingestor.CreateIssueRule(ctx, model.IssueRuleCreate{
		ServiceID: other, Kind: model.IssueRuleNewIssue, WindowSecs: 60,
	}, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("CreateIssueRule(no actor) error = %v, want ErrInvalidInput", err)
	}

	if err := fixture.ingestor.DeleteIssueRule(ctx, spike.ID, testOperator); !errors.Is(err, store.ErrIssueRuleNotFound) {
		t.Errorf("DeleteIssueRule(deleted) error = %v, want ErrIssueRuleNotFound", err)
	}

	if got := auditRows(t, fixture.db); len(got) != 4 {
		t.Errorf("audit rows after refused calls = %+v, want the same four", got)
	}
}

// seedNotifications stores one unread notification per listed service
// ("" for none) and returns the first id.
func seedNotifications(t *testing.T, db *store.DB, serviceIDs ...string) int64 {
	t.Helper()

	var first int64

	for _, serviceID := range serviceIDs {
		stored, err := store.NewNotificationStore(db).Insert(context.Background(), serviceID, "alert", "title", "body")
		if err != nil {
			t.Fatalf("Insert(notification) error = %v, want nil", err)
		}

		if first == 0 {
			first = stored.ID
		}
	}

	return first
}

// wantMarked fails unless a mark-read call changed want rows.
func wantMarked(t *testing.T, label string, want, marked int64, err error) {
	t.Helper()

	if err != nil || marked != want {
		t.Errorf("%s = %d, %v, want %d", label, marked, err, want)
	}
}

func TestMarkingNotificationsReadIsAudited(t *testing.T) {
	t.Parallel()

	fixture := newOperatorFixture(t)
	ctx := context.Background()
	granted, other := testfixture.GenericGranted, testfixture.GenericOther
	first := seedNotifications(t, fixture.db, granted, granted, granted, other, other, "")

	// One read, repeated, then an unknown id: only the call that
	// changed the flag is audited.
	for _, id := range []int64{first, first, 9999} {
		if err := fixture.sampler.MarkNotificationRead(ctx, id, testOperator); err != nil {
			t.Fatalf("MarkNotificationRead(%d) error = %v, want nil", id, err)
		}
	}

	// A scoped mark-all touches the listed services only.
	marked, err := fixture.sampler.MarkNotificationsReadForServices(ctx, []string{granted}, testOperator)
	wantMarked(t, "MarkNotificationsReadForServices()", 2, marked, err)

	unread, err := store.NewNotificationStore(fixture.db).CountUnread(ctx, "")
	wantMarked(t, "CountUnread() after the scoped mark-all", 3, unread, err)

	// A fleet-wide mark-all is attributed per service; with nothing
	// left unread, nothing more is written.
	marked, err = fixture.sampler.MarkNotificationsRead(ctx, "", testAdminActor)
	wantMarked(t, "MarkNotificationsRead(all)", 3, marked, err)

	marked, err = fixture.sampler.MarkNotificationsRead(ctx, other, testOperator)
	wantMarked(t, "MarkNotificationsRead(nothing unread)", 0, marked, err)

	wantAuditRows(t, fixture.db, []auditRow{
		{service: granted, actor: testOperator, action: model.AuditNotificationRead, detail: "marked notification 1 read"},
		{service: granted, actor: testOperator, action: model.AuditNotificationRead, detail: "marked 2 notifications read"},
		{actor: testAdminActor, action: model.AuditNotificationRead, detail: "marked 1 notifications read"},
		{service: other, actor: testAdminActor, action: model.AuditNotificationRead, detail: "marked 2 notifications read"},
	})
}

func TestMarkingNotificationsReadRejects(t *testing.T) {
	t.Parallel()

	fixture := newOperatorFixture(t)
	ctx := context.Background()
	first := seedNotifications(t, fixture.db, testfixture.GenericGranted)

	if err := fixture.sampler.MarkNotificationRead(ctx, first, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("MarkNotificationRead(no actor) error = %v, want ErrInvalidInput", err)
	}

	if _, err := fixture.sampler.MarkNotificationsRead(ctx, "", ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("MarkNotificationsRead(no actor) error = %v, want ErrInvalidInput", err)
	}

	if _, err := fixture.sampler.MarkNotificationsReadForServices(ctx, []string{testfixture.GenericGranted}, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("MarkNotificationsReadForServices(no actor) error = %v, want ErrInvalidInput", err)
	}

	if _, err := fixture.sampler.MarkNotificationsRead(ctx, "no-such-service", testOperator); !errors.Is(err, store.ErrServiceNotFound) {
		t.Errorf("MarkNotificationsRead(unknown service) error = %v, want ErrServiceNotFound", err)
	}

	if got := auditRows(t, fixture.db); len(got) != 0 {
		t.Errorf("audit rows after refused calls = %+v, want none", got)
	}
}

func TestManualDeployRecordIsAudited(t *testing.T) {
	t.Parallel()

	fixture := newOperatorFixture(t)
	ctx := context.Background()
	granted := testfixture.GenericGranted

	record := model.DeployRecord{
		ServiceID: granted, SHA: "abc1234", Actor: testOperator, Type: model.DeployManual, Outcome: model.DeploySuccess,
	}

	if _, err := fixture.deploys.RecordManual(ctx, record, testOperator); err != nil {
		t.Fatalf("RecordManual() error = %v, want nil", err)
	}

	// An admin may attribute the history entry to someone else; the
	// audit row still names the admin who recorded it.
	record.Actor = "release-bot"

	if _, err := fixture.deploys.RecordManual(ctx, record, testAdminActor); err != nil {
		t.Fatalf("RecordManual(attributed) error = %v, want nil", err)
	}

	wantAuditRows(t, fixture.db, []auditRow{
		{service: granted, actor: testOperator, action: model.AuditDeployRecord, detail: "recorded manual success of abc1234 (deploy 1)"},
		{
			service: granted, actor: testAdminActor, action: model.AuditDeployRecord,
			detail: "recorded manual success of abc1234 (deploy 2), attributed to release-bot",
		},
	})

	record.SHA = ""

	if _, err := fixture.deploys.RecordManual(ctx, record, testOperator); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("RecordManual(no sha) error = %v, want ErrInvalidInput", err)
	}

	record.SHA = "abc1234"

	if _, err := fixture.deploys.RecordManual(ctx, record, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("RecordManual(no recording user) error = %v, want ErrInvalidInput", err)
	}

	unaudited := NewDeploys(store.NewServiceStore(fixture.db), store.NewDeployStore(fixture.db))
	if _, err := unaudited.RecordManual(ctx, record, testOperator); err == nil {
		t.Error("RecordManual() without an audit service error = nil, want it refused")
	}

	if got := auditRows(t, fixture.db); len(got) != 2 {
		t.Errorf("audit rows after refused calls = %+v, want the same two", got)
	}
}
