package evidencepublisher

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
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

// PolicyVerifierConfig identifies the cluster this workload belongs to and the
// current (optionally previous) Trace Admission policy verification keys. The
// keys are public Ed25519 keys supplied by the existing chart Secret refs.
type PolicyVerifierConfig struct {
	AudienceClusterID string
	CurrentKeyID      string
	CurrentPublicKey  ed25519.PublicKey
	PreviousKeyID     string
	PreviousPublicKey ed25519.PublicKey
	Now               func() time.Time
}

// PolicyClientConfig configures a bearer-authenticated read of the frozen S3
// TraceEvidencePolicySnapshotV1 endpoint.
type PolicyClientConfig struct {
	BaseURL     string
	HTTPClient  *http.Client
	TokenSource AccessTokenSource
	Verifier    PolicyVerifierConfig
}

// PolicySnapshot is the verified policy state used by the publisher runtime.
// Signature is intentionally omitted: callers only receive verified fields.
type PolicySnapshot struct {
	Revision          uint64
	TraceAdmission    string
	EvidenceAdmission string
	IssuedAt          time.Time
	ExpiresAt         time.Time
	KeyID             string
	AudienceClusterID string
}

// PolicyClient strictly decodes and verifies the signed policy response before
// exposing it to a publisher. It never treats an unverified response as an
// admission decision.
type PolicyClient struct {
	url         string
	client      *http.Client
	tokenSource AccessTokenSource
	verifier    PolicyVerifierConfig
}

func NewPolicyClient(config PolicyClientConfig) (*PolicyClient, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || config.TokenSource == nil || !validVerifierConfig(config.Verifier) {
		return nil, errInvalidPolicyClient
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	if config.Verifier.Now == nil {
		config.Verifier.Now = time.Now
	}
	return &PolicyClient{
		url: strings.TrimRight(baseURL, "/") + traceEvidencePolicyPath, client: client,
		tokenSource: config.TokenSource, verifier: config.Verifier,
	}, nil
}

func validVerifierConfig(config PolicyVerifierConfig) bool {
	if strings.TrimSpace(config.AudienceClusterID) == "" || strings.TrimSpace(config.CurrentKeyID) == "" || len(config.CurrentPublicKey) != ed25519.PublicKeySize {
		return false
	}
	hasPreviousID := strings.TrimSpace(config.PreviousKeyID) != ""
	hasPreviousKey := len(config.PreviousPublicKey) != 0
	return hasPreviousID == hasPreviousKey && (!hasPreviousKey || len(config.PreviousPublicKey) == ed25519.PublicKeySize)
}

func (c *PolicyClient) Read(ctx context.Context) (PolicySnapshot, error) {
	if c == nil || c.client == nil || c.tokenSource == nil {
		return PolicySnapshot{}, errInvalidPolicyClient
	}
	token, err := c.tokenSource.Token(ctx)
	if err != nil {
		return PolicySnapshot{}, fmt.Errorf("get BKN Safe access token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return PolicySnapshot{}, errors.New("BKN Safe access token is empty")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return PolicySnapshot{}, fmt.Errorf("create trace evidence policy request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
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
	return c.verify(wire)
}

type policySnapshotWireFormat struct {
	ContractVersion   string    `json:"contract_version"`
	Revision          uint64    `json:"revision"`
	TraceAdmission    string    `json:"trace_admission"`
	EvidenceAdmission string    `json:"evidence_admission"`
	IssuedAt          time.Time `json:"issued_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	KeyID             string    `json:"key_id"`
	AudienceClusterID string    `json:"audience_cluster_id"`
	Signature         string    `json:"signature"`
}

func (c *PolicyClient) verify(wire policySnapshotWireFormat) (PolicySnapshot, error) {
	if wire.ContractVersion != "TraceEvidencePolicySnapshotV1" || wire.Revision == 0 || !validAdmissionMode(wire.TraceAdmission) || wire.EvidenceAdmission != wire.TraceAdmission || strings.TrimSpace(wire.KeyID) == "" || wire.AudienceClusterID != c.verifier.AudienceClusterID {
		return PolicySnapshot{}, errors.New("invalid trace evidence policy snapshot")
	}
	now := c.verifier.Now()
	if wire.IssuedAt.IsZero() || wire.ExpiresAt.IsZero() || now.Before(wire.IssuedAt) || !now.Before(wire.ExpiresAt) {
		return PolicySnapshot{}, errors.New("trace evidence policy snapshot is outside its validity window")
	}
	publicKey, ok := c.publicKeyFor(wire.KeyID)
	if !ok {
		return PolicySnapshot{}, errors.New("trace evidence policy snapshot has unknown key ID")
	}
	signature, err := rawPolicySignature(wire.Signature)
	if err != nil {
		return PolicySnapshot{}, err
	}
	canonical, err := json.Marshal(struct {
		ContractVersion   string    `json:"contract_version"`
		Revision          uint64    `json:"revision"`
		TraceAdmission    string    `json:"trace_admission"`
		EvidenceAdmission string    `json:"evidence_admission"`
		IssuedAt          time.Time `json:"issued_at"`
		ExpiresAt         time.Time `json:"expires_at"`
		KeyID             string    `json:"key_id"`
		AudienceClusterID string    `json:"audience_cluster_id"`
	}{wire.ContractVersion, wire.Revision, wire.TraceAdmission, wire.EvidenceAdmission, wire.IssuedAt, wire.ExpiresAt, wire.KeyID, wire.AudienceClusterID})
	if err != nil {
		return PolicySnapshot{}, fmt.Errorf("canonicalize trace evidence policy snapshot: %w", err)
	}
	if !ed25519.Verify(publicKey, canonical, signature) {
		return PolicySnapshot{}, errors.New("invalid trace evidence policy signature")
	}
	return PolicySnapshot{Revision: wire.Revision, TraceAdmission: wire.TraceAdmission, EvidenceAdmission: wire.EvidenceAdmission, IssuedAt: wire.IssuedAt, ExpiresAt: wire.ExpiresAt, KeyID: wire.KeyID, AudienceClusterID: wire.AudienceClusterID}, nil
}

func validAdmissionMode(mode string) bool { return mode == "enabled" || mode == "disabled" }

func (c *PolicyClient) publicKeyFor(keyID string) (ed25519.PublicKey, bool) {
	if keyID == c.verifier.CurrentKeyID {
		return c.verifier.CurrentPublicKey, true
	}
	if keyID == c.verifier.PreviousKeyID && len(c.verifier.PreviousPublicKey) == ed25519.PublicKeySize {
		return c.verifier.PreviousPublicKey, true
	}
	return nil, false
}

func rawPolicySignature(value string) ([]byte, error) {
	const prefix = "ed25519:"
	if !strings.HasPrefix(value, prefix) {
		return nil, errors.New("trace evidence policy signature has invalid prefix")
	}
	signature, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, errors.New("trace evidence policy signature is invalid")
	}
	return signature, nil
}
