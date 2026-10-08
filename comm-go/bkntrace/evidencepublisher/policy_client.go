package evidencepublisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const traceEvidencePolicyPath = "/api/agent-observability/v1/internal/trace-evidence/policy"

var errInvalidPolicyClient = errors.New("invalid evidence publisher policy client")

// PolicyClientConfig configures an internal Trace Admission policy read.
type PolicyClientConfig struct {
	BaseURL    string
	HTTPClient *http.Client
	Now        func() time.Time
}

// PolicySnapshot is the validated policy state used by the publisher runtime.
type PolicySnapshot struct {
	Revision          uint64
	TraceAdmission    string
	EvidenceAdmission string
	IssuedAt          time.Time
	ExpiresAt         time.Time
}

// PolicyClient validates the revision, admission modes and validity window
// returned by the internal policy endpoint.
type PolicyClient struct {
	url    string
	client *http.Client
	now    func() time.Time
}

func NewPolicyClient(config PolicyClientConfig) (*PolicyClient, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errInvalidPolicyClient
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &PolicyClient{
		url: strings.TrimRight(baseURL, "/") + traceEvidencePolicyPath, client: client,
		now: config.Now,
	}, nil
}

func (c *PolicyClient) Read(ctx context.Context) (PolicySnapshot, error) {
	if c == nil || c.client == nil {
		return PolicySnapshot{}, errInvalidPolicyClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return PolicySnapshot{}, fmt.Errorf("create trace evidence policy request: %w", err)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return PolicySnapshot{}, fmt.Errorf("read trace evidence policy: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return PolicySnapshot{}, fmt.Errorf("read trace evidence policy: unexpected status %d", response.StatusCode)
	}
	var wire policySnapshotWireFormat
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return PolicySnapshot{}, fmt.Errorf("decode trace evidence policy: %w", err)
	}
	if err := requireSingleJSONValue(decoder); err != nil {
		return PolicySnapshot{}, err
	}
	return c.validate(wire)
}

type policySnapshotWireFormat struct {
	ContractVersion   string    `json:"contract_version"`
	Revision          uint64    `json:"revision"`
	TraceAdmission    string    `json:"trace_admission"`
	EvidenceAdmission string    `json:"evidence_admission"`
	IssuedAt          time.Time `json:"issued_at"`
	ExpiresAt         time.Time `json:"expires_at"`
}

func (c *PolicyClient) validate(wire policySnapshotWireFormat) (PolicySnapshot, error) {
	if wire.ContractVersion != "TraceEvidencePolicySnapshotV1" || wire.Revision == 0 || !validAdmissionMode(wire.TraceAdmission) || wire.EvidenceAdmission != wire.TraceAdmission {
		return PolicySnapshot{}, errors.New("invalid trace evidence policy snapshot")
	}
	now := c.now()
	if wire.IssuedAt.IsZero() || wire.ExpiresAt.IsZero() || !wire.ExpiresAt.After(wire.IssuedAt) || now.Before(wire.IssuedAt) || !now.Before(wire.ExpiresAt) {
		return PolicySnapshot{}, errors.New("trace evidence policy snapshot is outside its validity window")
	}
	return PolicySnapshot{Revision: wire.Revision, TraceAdmission: wire.TraceAdmission, EvidenceAdmission: wire.EvidenceAdmission, IssuedAt: wire.IssuedAt, ExpiresAt: wire.ExpiresAt}, nil
}

func validAdmissionMode(mode string) bool { return mode == "enabled" || mode == "disabled" }
