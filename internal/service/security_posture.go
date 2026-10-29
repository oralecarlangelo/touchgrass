package service

import (
	"slices"
	"strings"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// EvaluatePosture produces a complete, deterministic rule set from normalized
// secret-safe container configuration.
func EvaluatePosture(
	instance model.ContainerInstance,
	inspection model.SafeInspection,
	policy model.SecurityPolicy,
) []model.SecurityObservation {
	containerID := instance.ID
	observations := make([]model.SecurityObservation, 0, 15)
	add := func(rule string, severity model.SecuritySeverity, detected bool, evidence map[string]string) {
		observations = append(observations, model.SecurityObservation{
			ContainerID: containerID, RuleID: rule, Category: postureCategory(rule),
			Severity: severity, Confidence: model.SecurityConfidenceHigh,
			Detected: detected, CoverageComplete: true, Evidence: evidence,
		})
	}

	add("posture.privileged", model.SecuritySeverityCritical,
		inspection.Privileged && !policy.AllowPrivileged, boolEvidence("privileged", inspection.Privileged))
	add("posture.host_pid", model.SecuritySeverityCritical,
		inspection.PIDMode == "host" && !policy.AllowHostPID, valueEvidence("pid_mode", inspection.PIDMode))
	add("posture.host_ipc", model.SecuritySeverityHigh,
		inspection.IPCMode == "host", valueEvidence("ipc_mode", inspection.IPCMode))
	add("posture.host_network", model.SecuritySeverityHigh,
		inspection.NetworkMode == "host" && !policy.AllowHostNetwork, valueEvidence("network_mode", inspection.NetworkMode))
	add("posture.docker_socket", model.SecuritySeverityCritical,
		inspection.HasDockerSocket && !policy.AllowDockerSocket, boolEvidence("docker_socket", inspection.HasDockerSocket))
	root := inspection.User == "" || inspection.User == "0" || strings.EqualFold(inspection.User, "root")
	add("posture.root_user", model.SecuritySeverityHigh, root && !policy.AllowRoot, boolEvidence("root_user", root))
	add("posture.writable_rootfs", model.SecuritySeverityMedium,
		!inspection.ReadonlyRootFS && !policy.AllowWritableRootFS,
		boolEvidence("writable_rootfs", !inspection.ReadonlyRootFS))

	unexpectedCaps := differenceFold(inspection.CapAdd, policy.AllowedCapabilities)
	add("posture.capabilities", model.SecuritySeverityHigh, len(unexpectedCaps) > 0,
		valueEvidence("unexpected_capabilities", strings.Join(unexpectedCaps, ",")))
	add("posture.memory_unlimited", model.SecuritySeverityMedium, inspection.MemoryBytes <= 0,
		boolEvidence("memory_limit_missing", inspection.MemoryBytes <= 0))
	add("posture.cpu_unlimited", model.SecuritySeverityMedium, inspection.NanoCPUs <= 0,
		boolEvidence("cpu_limit_missing", inspection.NanoCPUs <= 0))
	add("posture.pids_unlimited", model.SecuritySeverityMedium,
		inspection.PIDsLimit == nil || *inspection.PIDsLimit <= 0,
		boolEvidence("pids_limit_missing", inspection.PIDsLimit == nil || *inspection.PIDsLimit <= 0))

	unexpectedPorts := differenceFold(inspection.PublishedPorts, policy.AllowedPublishedPorts)
	add("posture.published_ports", model.SecuritySeverityHigh, len(unexpectedPorts) > 0,
		valueEvidence("unexpected_ports", strings.Join(unexpectedPorts, ",")))
	mutableTag := instance.Image != "" && !strings.Contains(instance.Image, "@sha256:")
	add("image.mutable_tag", model.SecuritySeverityMedium, mutableTag,
		boolEvidence("mutable_tag", mutableTag))
	imageDrift := instance.ImageFingerprint != "" && instance.ImageFingerprint != instance.ImageID
	add("image.drift", model.SecuritySeverityHigh, imageDrift,
		boolEvidence("image_fingerprint_changed", imageDrift))
	configurationDrift := instance.ConfigurationFingerprint != "" &&
		instance.ConfigurationFingerprint != store.FingerprintInspection(inspection)
	add("configuration.drift", model.SecuritySeverityHigh, configurationDrift,
		boolEvidence("configuration_fingerprint_changed", configurationDrift))
	return observations
}

func postureCategory(rule string) model.SecurityCategory {
	if strings.HasSuffix(rule, ".drift") {
		return model.SecurityCategoryDrift
	}
	return model.SecurityCategoryPosture
}

func differenceFold(values, allowed []string) []string {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[strings.ToUpper(strings.TrimSpace(value))] = struct{}{}
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := allowedSet[strings.ToUpper(strings.TrimSpace(value))]; !ok {
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return result
}

func boolEvidence(key string, value bool) map[string]string {
	if value {
		return map[string]string{key: "true"}
	}
	return map[string]string{key: "false"}
}

func valueEvidence(key, value string) map[string]string {
	return map[string]string{key: value}
}
