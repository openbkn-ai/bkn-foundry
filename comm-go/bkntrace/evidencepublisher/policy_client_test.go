package evidencepublisher

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPolicyClientReadsAndVerifiesFrozenSnapshot(t *testing.T) {
	now := time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := signedPolicySnapshot(t, privateKey, policySnapshotForTest(now))
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "https://trace.internal/api/agent-observability/v1/internal/trace-evidence/policy" {
			t.Fatalf("unexpected policy request: %s %s", request.Method, request.URL)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer workload-token" {
			t.Fatalf("Authorization = %q", got)
		}
		return policyResponse(body), nil
	})}
	policy, err := NewPolicyClient(PolicyClientConfig{
		BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token"),
		Verifier: PolicyVerifierConfig{AudienceClusterID: "cluster-a", CurrentKeyID: "key-1", CurrentPublicKey: publicKey, Now: func() time.Time { return now }},
	})
	if err != nil {
		t.Fatalf("NewPolicyClient() error = %v", err)
	}
	snapshot, err := policy.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if snapshot.Revision != 42 || snapshot.TraceAdmission != "enabled" || snapshot.EvidenceAdmission != "enabled" {
		t.Fatalf("Read() snapshot = %+v", snapshot)
	}
}

func TestPolicyClientRejectsDivergentAdmissionModes(t *testing.T) {
	now := time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := policySnapshotForTest(now)
	snapshot.EvidenceAdmission = "disabled"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return policyResponse(signedPolicySnapshot(t, privateKey, snapshot)), nil
	})}
	policy, err := NewPolicyClient(PolicyClientConfig{
		BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token"),
		Verifier: PolicyVerifierConfig{AudienceClusterID: "cluster-a", CurrentKeyID: "key-1", CurrentPublicKey: publicKey, Now: func() time.Time { return now }},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Read(context.Background()); err == nil {
		t.Fatal("Read() error = nil; want divergent admission modes rejected")
	}
}

func TestPolicyClientRejectsExpiredUnknownAndTamperedSnapshots(t *testing.T) {
	now := time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	valid := policySnapshotForTest(now)
	cases := []struct {
		name string
		body string
	}{
		{name: "expired", body: signedPolicySnapshot(t, privateKey, func() policySnapshotWire { snapshot := valid; snapshot.ExpiresAt = now; return snapshot }())},
		{name: "inverted validity window", body: signedPolicySnapshot(t, privateKey, func() policySnapshotWire {
			snapshot := valid
			snapshot.IssuedAt = now.Add(30 * time.Second)
			snapshot.ExpiresAt = now.Add(10 * time.Second)
			return snapshot
		}())},
		{name: "unknown field", body: strings.TrimSuffix(signedPolicySnapshot(t, privateKey, valid), "}") + `,"admission_mode":"enabled"}`},
		{name: "tampered signature", body: strings.Replace(signedPolicySnapshot(t, privateKey, valid), `"revision":42`, `"revision":43`, 1)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return policyResponse(testCase.body), nil
			})}
			policy, err := NewPolicyClient(PolicyClientConfig{
				BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token"),
				Verifier: PolicyVerifierConfig{AudienceClusterID: "cluster-a", CurrentKeyID: "key-1", CurrentPublicKey: publicKey, Now: func() time.Time { return now }},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := policy.Read(context.Background()); err == nil {
				t.Fatal("Read() error = nil; want invalid snapshot rejected")
			}
		})
	}
}

func TestPolicyClientAcceptsConfiguredPreviousKey(t *testing.T) {
	now := time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)
	_, currentPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	previousPublicKey, previousPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := policySnapshotForTest(now)
	snapshot.KeyID = "key-0"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return policyResponse(signedPolicySnapshot(t, previousPrivateKey, snapshot)), nil
	})}
	policy, err := NewPolicyClient(PolicyClientConfig{
		BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token"),
		Verifier: PolicyVerifierConfig{AudienceClusterID: "cluster-a", CurrentKeyID: "key-1", CurrentPublicKey: currentPrivateKey.Public().(ed25519.PublicKey), PreviousKeyID: "key-0", PreviousPublicKey: previousPublicKey, Now: func() time.Time { return now }},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Read(context.Background()); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
}

type policySnapshotWire struct {
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

func policySnapshotForTest(now time.Time) policySnapshotWire {
	return policySnapshotWire{
		ContractVersion: "TraceEvidencePolicySnapshotV1", Revision: 42,
		TraceAdmission: "enabled", EvidenceAdmission: "enabled",
		IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute),
		KeyID: "key-1", AudienceClusterID: "cluster-a",
	}
}

func signedPolicySnapshot(t *testing.T, privateKey ed25519.PrivateKey, snapshot policySnapshotWire) string {
	t.Helper()
	canonical := struct {
		ContractVersion   string    `json:"contract_version"`
		Revision          uint64    `json:"revision"`
		TraceAdmission    string    `json:"trace_admission"`
		EvidenceAdmission string    `json:"evidence_admission"`
		IssuedAt          time.Time `json:"issued_at"`
		ExpiresAt         time.Time `json:"expires_at"`
		KeyID             string    `json:"key_id"`
		AudienceClusterID string    `json:"audience_cluster_id"`
	}{snapshot.ContractVersion, snapshot.Revision, snapshot.TraceAdmission, snapshot.EvidenceAdmission, snapshot.IssuedAt, snapshot.ExpiresAt, snapshot.KeyID, snapshot.AudienceClusterID}
	bytes, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Signature = "ed25519:" + base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, bytes))
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func policyResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
