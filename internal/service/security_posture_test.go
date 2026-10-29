package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func TestPostureRuleMatrix(t *testing.T) {
	t.Parallel()
	limit := int64(64)
	secure := model.SafeInspection{
		User: "1000", ReadonlyRootFS: true, MemoryBytes: 1, NanoCPUs: 1,
		PIDsLimit: &limit, CapAdd: []string{}, Mounts: []model.SafeMount{},
	}
	secureInstance := model.ContainerInstance{ID: "container", Image: "repo@sha256:abc", ImageID: "sha256:abc"}
	secureInstance.ImageFingerprint = secureInstance.ImageID
	secureInstance.ConfigurationFingerprint = store.FingerprintInspection(secure)

	tests := []struct {
		name       string
		mutate     func(*model.ContainerInstance, *model.SafeInspection)
		policy     model.SecurityPolicy
		rule       string
		severity   model.SecuritySeverity
		overridden bool
	}{
		{name: "privileged", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.Privileged = true }, rule: "posture.privileged", severity: model.SecuritySeverityCritical},
		{name: "privileged allowed", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.Privileged = true }, policy: model.SecurityPolicy{AllowPrivileged: true}, rule: "posture.privileged", overridden: true},
		{name: "host pid", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.PIDMode = "host" }, rule: "posture.host_pid", severity: model.SecuritySeverityCritical},
		{name: "host pid allowed", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.PIDMode = "host" }, policy: model.SecurityPolicy{AllowHostPID: true}, rule: "posture.host_pid", overridden: true},
		{name: "host ipc", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.IPCMode = "host" }, rule: "posture.host_ipc", severity: model.SecuritySeverityHigh},
		{name: "host network", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.NetworkMode = "host" }, rule: "posture.host_network", severity: model.SecuritySeverityHigh},
		{name: "host network allowed", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.NetworkMode = "host" }, policy: model.SecurityPolicy{AllowHostNetwork: true}, rule: "posture.host_network", overridden: true},
		{name: "docker socket", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.HasDockerSocket = true }, rule: "posture.docker_socket", severity: model.SecuritySeverityCritical},
		{name: "docker socket allowed", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.HasDockerSocket = true }, policy: model.SecurityPolicy{AllowDockerSocket: true}, rule: "posture.docker_socket", overridden: true},
		{name: "root", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.User = "root" }, rule: "posture.root_user", severity: model.SecuritySeverityHigh},
		{name: "root allowed", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.User = "0" }, policy: model.SecurityPolicy{AllowRoot: true}, rule: "posture.root_user", overridden: true},
		{name: "writable root", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.ReadonlyRootFS = false }, rule: "posture.writable_rootfs", severity: model.SecuritySeverityMedium},
		{name: "writable root allowed", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.ReadonlyRootFS = false }, policy: model.SecurityPolicy{AllowWritableRootFS: true}, rule: "posture.writable_rootfs", overridden: true},
		{name: "capability", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.CapAdd = []string{"NET_ADMIN"} }, rule: "posture.capabilities", severity: model.SecuritySeverityHigh},
		{name: "capability allowed", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.CapAdd = []string{"NET_ADMIN"} }, policy: model.SecurityPolicy{AllowedCapabilities: []string{"NET_ADMIN"}}, rule: "posture.capabilities", overridden: true},
		{name: "memory unlimited", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.MemoryBytes = 0 }, rule: "posture.memory_unlimited", severity: model.SecuritySeverityMedium},
		{name: "cpu unlimited", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.NanoCPUs = 0 }, rule: "posture.cpu_unlimited", severity: model.SecuritySeverityMedium},
		{name: "pids unlimited", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) { i.PIDsLimit = nil }, rule: "posture.pids_unlimited", severity: model.SecuritySeverityMedium},
		{name: "port", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) {
			i.PublishedPorts = []string{"0.0.0.0:8080->80/tcp"}
		}, rule: "posture.published_ports", severity: model.SecuritySeverityHigh},
		{name: "port allowed", mutate: func(_ *model.ContainerInstance, i *model.SafeInspection) {
			i.PublishedPorts = []string{"127.0.0.1:8080->80/tcp"}
		}, policy: model.SecurityPolicy{AllowedPublishedPorts: []string{"127.0.0.1:8080->80/tcp"}}, rule: "posture.published_ports", overridden: true},
		{name: "mutable tag", mutate: func(c *model.ContainerInstance, _ *model.SafeInspection) { c.Image = "repo:latest" }, rule: "image.mutable_tag", severity: model.SecuritySeverityMedium},
		{name: "image drift", mutate: func(c *model.ContainerInstance, _ *model.SafeInspection) { c.ImageFingerprint = "sha256:expected" }, rule: "image.drift", severity: model.SecuritySeverityHigh},
		{name: "configuration drift", mutate: func(c *model.ContainerInstance, _ *model.SafeInspection) { c.ConfigurationFingerprint = "expected" }, rule: "configuration.drift", severity: model.SecuritySeverityHigh},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance := secureInstance
			inspection := secure
			inspection.CapAdd = append([]string{}, secure.CapAdd...)
			test.mutate(&instance, &inspection)
			got := observationByRule(EvaluatePosture(instance, inspection, test.policy), test.rule)
			if got.RuleID == "" {
				t.Fatalf("rule %q missing", test.rule)
			}
			if got.Detected == test.overridden {
				t.Errorf("Detected = %v, want %v", got.Detected, !test.overridden)
			}
			if !test.overridden && got.Severity != test.severity {
				t.Errorf("Severity = %q, want %q", got.Severity, test.severity)
			}
		})
	}
}

func TestPostureScannerDockerSocketIsCritical(t *testing.T) {
	t.Parallel()
	got := observationByRule(EvaluatePosture(model.ContainerInstance{ID: "c"},
		model.SafeInspection{HasDockerSocket: true}, model.SecurityPolicy{}), "posture.docker_socket")
	if !got.Detected || got.Severity != model.SecuritySeverityCritical {
		t.Fatalf("docker socket observation = %+v, want critical detection", got)
	}
}

func TestPostureScannerDoesNotPersistEnvironmentOrMountSource(t *testing.T) {
	t.Parallel()
	observations := EvaluatePosture(model.ContainerInstance{ID: "c"}, model.SafeInspection{
		HasDockerSocket: true,
		Mounts:          []model.SafeMount{{Type: "bind", Destination: "/var/run/docker.sock", Writable: true}},
	}, model.SecurityPolicy{})
	encoded, err := json.Marshal(observations)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"SECRET=", "/host/private", "mount_source", "environment"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Errorf("observations expose forbidden value %q: %s", forbidden, text)
		}
	}
}

func observationByRule(values []model.SecurityObservation, rule string) model.SecurityObservation {
	for _, value := range values {
		if value.RuleID == rule {
			return value
		}
	}
	return model.SecurityObservation{}
}
