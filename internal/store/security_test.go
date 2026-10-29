package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestSecurityEvidenceMetadataCap(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	_, err := NewSecurityStore(db).Evaluate(context.Background(), model.SecurityEvaluation{Observation: model.SecurityObservation{
		ContainerID: "c", RuleID: "posture.cap", Category: model.SecurityCategoryPosture,
		Severity: model.SecuritySeverityHigh, Confidence: model.SecurityConfidenceHigh,
		Detected: true, CoverageComplete: true, Evidence: map[string]string{"oversized": strings.Repeat("x", (32<<10)+1)},
	}})
	if err == nil {
		t.Fatal("Evaluate() error = nil, want evidence byte cap")
	}
}

func TestSecurityStoreActivationDoesNotAutoApproveExisting(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	containers := NewContainerStore(db)
	seedSecurityContainer(t, containers, "existing", model.ContainerClassificationUnknown)
	security := NewSecurityStore(db)
	activation, err := security.Activate(context.Background(), "admin")
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if activation.ActivatedAt == nil || activation.ActivatedBy != "admin" {
		t.Errorf("activation = %+v", activation)
	}
	got, err := containers.Get(context.Background(), "existing")
	if err != nil {
		t.Fatal(err)
	}
	if got.Classification != model.ContainerClassificationUnknown {
		t.Errorf("classification = %q, want unknown", got.Classification)
	}
}

func TestApproveContainerRecordsFingerprintAndActor(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	containers := NewContainerStore(db)
	seedSecurityContainer(t, containers, "approve-id", model.ContainerClassificationUnknown)
	security := NewSecurityStore(db)
	approvedAt := time.Now().UTC().Truncate(time.Millisecond)
	baseline, err := security.Approve(context.Background(), model.ContainerApproval{
		ContainerID: "approve-id", ImageFingerprint: "sha256:image",
		ConfigurationFingerprint: "config-hash", Actor: "alice", ApprovedAt: approvedAt,
	})
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if baseline.ApprovedBy != "alice" || baseline.ImageFingerprint != "sha256:image" ||
		baseline.ConfigurationFingerprint != "config-hash" || !baseline.ApprovedAt.Equal(approvedAt) {
		t.Errorf("baseline = %+v", baseline)
	}
}

func TestIgnoreContainerRequiresReason(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	containers := NewContainerStore(db)
	seedSecurityContainer(t, containers, "ignore-id", model.ContainerClassificationUnknown)
	_, err := NewSecurityStore(db).Ignore(context.Background(), model.ContainerIgnore{
		ContainerID: "ignore-id", NonExpiring: true, Actor: "admin",
	})
	if err == nil {
		t.Fatal("Ignore() error = nil, want reason validation")
	}
}

func TestExpiredIgnoreReturnsUnknown(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	containers := NewContainerStore(db)
	seedSecurityContainer(t, containers, "expired-id", model.ContainerClassificationUnknown)
	security := NewSecurityStore(db)
	expires := time.Now().UTC().Add(time.Minute)
	if _, err := security.Ignore(context.Background(), model.ContainerIgnore{
		ContainerID: "expired-id", Reason: "temporary test", ExpiresAt: &expires, Actor: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	classification, err := security.Classify(context.Background(), model.ContainerInstance{
		ID: "expired-id", ImageFingerprint: "image", ConfigurationFingerprint: "config",
	}, expires.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if classification != model.ContainerClassificationUnknown {
		t.Errorf("classification = %q, want unknown after expiry", classification)
	}
}

func TestClassificationPrecedence(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	containers := NewContainerStore(db)
	seedSecurityContainer(t, containers, "precedence-id", model.ContainerClassificationUnknown)
	security := NewSecurityStore(db)
	if _, err := security.Approve(context.Background(), model.ContainerApproval{
		ContainerID: "precedence-id", ImageFingerprint: "image", ConfigurationFingerprint: "config", Actor: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := security.Ignore(context.Background(), model.ContainerIgnore{
		ContainerID: "precedence-id", Reason: "known helper", NonExpiring: true, Actor: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	classification, err := security.Classify(context.Background(), model.ContainerInstance{
		ID: "precedence-id", ImageFingerprint: "image", ConfigurationFingerprint: "config",
	}, now)
	if err != nil || classification != model.ContainerClassificationIgnored {
		t.Fatalf("ignored classification = %q, %v", classification, err)
	}
	classification, err = security.Classify(context.Background(), model.ContainerInstance{
		ID: "precedence-id", ServiceID: "tn-api", ImageFingerprint: "image", ConfigurationFingerprint: "config",
	}, now)
	if err != nil || classification != model.ContainerClassificationManaged {
		t.Fatalf("managed classification = %q, %v", classification, err)
	}
}

func seedSecurityContainer(t *testing.T, containers *ContainerStore, id string, classification model.ContainerClassification) {
	t.Helper()
	if err := containers.Reconcile(context.Background(), model.InventoryBatch{ObservedAt: time.Now().UTC(), Complete: true,
		Instances: []model.ContainerInstance{{ID: id, Name: id, Classification: classification}}}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
}
