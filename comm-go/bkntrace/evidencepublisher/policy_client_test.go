package evidencepublisher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPolicyClientReadsUnsignedSnapshotWithoutCredentials(t *testing.T) {
	now := time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)
	body := unsignedPolicySnapshot(t, policySnapshotForTest(now))
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "https://trace.internal/api/agent-observability/v1/internal/trace-evidence/policy" {
			t.Fatalf("unexpected policy request: %s %s", request.Method, request.URL)
		}
		if got := request.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q", got)
		}
		return policyResponse(body), nil
	})}
	policy, err := NewPolicyClient(PolicyClientConfig{
		BaseURL: "https://trace.internal", HTTPClient: client,
		Now: func() time.Time { return now },
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
	snapshot := policySnapshotForTest(now)
	snapshot.EvidenceAdmission = "disabled"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return policyResponse(unsignedPolicySnapshot(t, snapshot)), nil
	})}
	policy, err := NewPolicyClient(PolicyClientConfig{
		BaseURL: "https://trace.internal", HTTPClient: client,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Read(context.Background()); err == nil {
		t.Fatal("Read() error = nil; want divergent admission modes rejected")
	}
}

func TestPolicyClientRejectsExpiredUnknownAndInvalidSnapshots(t *testing.T) {
	now := time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)
	valid := policySnapshotForTest(now)
	cases := []struct {
		name string
		body string
	}{
		{name: "expired", body: unsignedPolicySnapshot(t, func() policySnapshotWireFormat { snapshot := valid; snapshot.ExpiresAt = now; return snapshot }())},
		{name: "inverted validity window", body: unsignedPolicySnapshot(t, func() policySnapshotWireFormat {
			snapshot := valid
			snapshot.IssuedAt = now.Add(30 * time.Second)
			snapshot.ExpiresAt = now.Add(10 * time.Second)
			return snapshot
		}())},
		{name: "future issued time", body: unsignedPolicySnapshot(t, func() policySnapshotWireFormat {
			snapshot := valid
			snapshot.IssuedAt = now.Add(30 * time.Second)
			return snapshot
		}())},
		{name: "invalid contract", body: strings.Replace(unsignedPolicySnapshot(t, valid), "TraceEvidencePolicySnapshotV1", "InvalidSnapshot", 1)},
		{name: "obsolete signature field", body: strings.TrimSuffix(unsignedPolicySnapshot(t, valid), "}") + `,"signature":"unused"}`},
		{name: "multiple JSON values", body: unsignedPolicySnapshot(t, valid) + " {}"},
		{name: "unknown field", body: strings.TrimSuffix(unsignedPolicySnapshot(t, valid), "}") + `,"admission_mode":"enabled"}`},
		{name: "invalid revision", body: strings.Replace(unsignedPolicySnapshot(t, valid), `"revision":42`, `"revision":0`, 1)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return policyResponse(testCase.body), nil
			})}
			policy, err := NewPolicyClient(PolicyClientConfig{
				BaseURL: "https://trace.internal", HTTPClient: client,
				Now: func() time.Time { return now },
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

func policySnapshotForTest(now time.Time) policySnapshotWireFormat {
	return policySnapshotWireFormat{ContractVersion: "TraceEvidencePolicySnapshotV1", Revision: 42, TraceAdmission: "enabled", EvidenceAdmission: "enabled", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute)}
}
func unsignedPolicySnapshot(t *testing.T, snapshot policySnapshotWireFormat) string {
	t.Helper()
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
func policyResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
