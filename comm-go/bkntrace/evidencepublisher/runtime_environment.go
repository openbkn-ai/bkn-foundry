package evidencepublisher

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"strings"
)

const (
	traceAdmissionPolicySuffix        = "/api/agent-observability/v1/internal/trace-evidence/policy"
	traceAdmissionConfigurationSuffix = "/api/agent-observability/v1/trace-evidence-configuration"
	traceAdmissionHeartbeatSuffix     = "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat"
	traceAdmissionACKSuffix           = "/api/agent-observability/v1/internal/trace-evidence/operations"
)

// NewPublisherRuntimeFromEnvironment consumes the existing S3
// TRACE_ADMISSION_* workload profile. It never provides defaults for an
// identity, credential, endpoint, audience, or verifier key.
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
	current, err := decodeTraceAdmissionPublicKey(get("TRACE_ADMISSION_CURRENT_PUBLIC_KEY"))
	if err != nil {
		return nil, err
	}
	previousText := get("TRACE_ADMISSION_PREVIOUS_PUBLIC_KEY")
	previous := ed25519.PublicKey(nil)
	if previousText != "" {
		previous, err = decodeTraceAdmissionPublicKey(previousText)
		if err != nil {
			return nil, err
		}
	}
	tokens, err := NewOAuthTokenSource(OAuthTokenConfig{TokenURL: get("TRACE_ADMISSION_TOKEN_URL"), ClientID: get("TRACE_ADMISSION_CLIENT_ID"), ClientSecret: os.Getenv("TRACE_ADMISSION_CLIENT_SECRET")})
	if err != nil {
		return nil, err
	}
	policy, err := NewPolicyClient(PolicyClientConfig{BaseURL: policyBase, TokenSource: tokens, Verifier: PolicyVerifierConfig{AudienceClusterID: get("TRACE_ADMISSION_AUDIENCE"), CurrentKeyID: get("TRACE_ADMISSION_CURRENT_KEY_ID"), CurrentPublicKey: current, PreviousKeyID: get("TRACE_ADMISSION_PREVIOUS_KEY_ID"), PreviousPublicKey: previous}})
	if err != nil {
		return nil, err
	}
	configuration, err := NewConfigurationClient(ConfigurationClientConfig{BaseURL: configurationBase, TokenSource: tokens})
	if err != nil {
		return nil, err
	}
	control, err := NewControlClient(ControlClientConfig{BaseURL: controlBase, TokenSource: tokens})
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

func decodeTraceAdmissionPublicKey(value string) (ed25519.PublicKey, error) {
	key, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(value)
	}
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("invalid Trace Admission public key")
	}
	return ed25519.PublicKey(key), nil
}
