package evidencepublisher

import (
	"context"
	"errors"
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
	policyBase, err := traceAdmissionBaseURL(get("TRACE_ADMISSION_POLICY_URL"), traceAdmissionPolicySuffix)
	if err != nil {
		return nil, err
	}
	configurationBase, err := traceAdmissionBaseURL(get("TRACE_ADMISSION_CONFIGURATION_URL"), traceAdmissionConfigurationSuffix)
	if err != nil {
		return nil, err
	}
	controlBase, err := traceAdmissionBaseURL(get("TRACE_ADMISSION_HEARTBEAT_URL"), traceAdmissionHeartbeatSuffix)
	if err != nil {
		return nil, err
	}
	ackBase, err := traceAdmissionBaseURL(get("TRACE_ADMISSION_ACK_URL_BASE"), traceAdmissionACKSuffix)
	if err != nil || ackBase != controlBase {
		return nil, errors.New("invalid TRACE_ADMISSION_ACK_URL_BASE")
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

func traceAdmissionBaseURL(value, suffix string) (string, error) {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.RawQuery != "" || !strings.HasSuffix(parsed.Path, suffix) {
		return "", errors.New("invalid Trace Admission endpoint configuration")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, suffix)
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}
