package evidencepublisher

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
)

const (
	traceAdmissionPolicySuffix        = "/api/agent-observability/v1/internal/trace-evidence/policy"
	traceAdmissionConfigurationSuffix = "/api/agent-observability/v1/internal/trace-evidence/configuration"
	traceAdmissionHeartbeatSuffix     = "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat"
	traceAdmissionACKSuffix           = "/api/agent-observability/v1/internal/trace-evidence/operations"
)

// NewPublisherRuntimeFromEnvironment consumes the existing S3
// TRACE_ADMISSION_* workload profile. It never provides defaults for a
// workload identity or internal endpoint.
func NewPublisherRuntimeFromEnvironment(ctx context.Context, publisher Config, sender Sender) (*PublisherRuntime, error) {
	get := func(name string) string { return strings.TrimSpace(os.Getenv(name)) }
	policyBase, err := traceAdmissionBaseURL("TRACE_ADMISSION_POLICY_URL", get("TRACE_ADMISSION_POLICY_URL"), traceAdmissionPolicySuffix)
	if err != nil {
		return nil, err
	}
	configurationBase, err := traceAdmissionBaseURL("TRACE_ADMISSION_CONFIGURATION_URL", get("TRACE_ADMISSION_CONFIGURATION_URL"), traceAdmissionConfigurationSuffix)
	if err != nil {
		return nil, err
	}
	controlBase, err := traceAdmissionBaseURL("TRACE_ADMISSION_HEARTBEAT_URL", get("TRACE_ADMISSION_HEARTBEAT_URL"), traceAdmissionHeartbeatSuffix)
	if err != nil {
		return nil, err
	}
	ackBase, err := traceAdmissionBaseURL("TRACE_ADMISSION_ACK_URL_BASE", get("TRACE_ADMISSION_ACK_URL_BASE"), traceAdmissionACKSuffix)
	if err != nil {
		return nil, err
	}
	if ackBase != controlBase {
		return nil, fmt.Errorf("TRACE_ADMISSION_ENDPOINT_INVALID: variable=TRACE_ADMISSION_ACK_URL_BASE stage=validate reason=base_mismatch expected=same base as TRACE_ADMISSION_HEARTBEAT_URL")
	}
	policy, err := NewPolicyClient(PolicyClientConfig{BaseURL: policyBase})
	if err != nil {
		return nil, err
	}
	configuration, err := NewConfigurationClient(ConfigurationClientConfig{BaseURL: configurationBase})
	if err != nil {
		return nil, err
	}
	control, err := NewControlClient(ControlClientConfig{BaseURL: controlBase})
	if err != nil {
		return nil, err
	}
	return NewPublisherRuntime(ctx, PublisherRuntimeConfig{Publisher: publisher, Sender: sender, Policy: policy, Configuration: configuration, Control: control})
}

func traceAdmissionBaseURL(name, value, suffix string) (string, error) {
	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		// URL parse errors include the raw input, which may contain credentials.
		return "", fmt.Errorf("TRACE_ADMISSION_ENDPOINT_INVALID: variable=%s stage=parse reason=invalid_url expected_suffix=%q", name, suffix)
	}
	reason := ""
	switch {
	case parsed.Scheme == "":
		reason = "missing_scheme"
	case parsed.Host == "":
		reason = "missing_host"
	case parsed.RawQuery != "":
		reason = "query_not_allowed"
	case !strings.HasSuffix(parsed.Path, suffix):
		reason = "path_suffix_mismatch"
	}
	if reason != "" {
		safeURL := *parsed
		safeURL.User, safeURL.RawQuery, safeURL.Fragment = nil, "", ""
		safeURL.ForceQuery = false
		return "", fmt.Errorf("TRACE_ADMISSION_ENDPOINT_INVALID: variable=%s stage=validate reason=%s url=%q path=%q expected_suffix=%q", name, reason, safeURL.String(), parsed.Path, suffix)
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, suffix)
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}
