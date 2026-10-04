package evidencepublisher

import "testing"

func TestCaptureDisabledDistinguishesPolicyFromInfrastructureFailure(t *testing.T) {
	r := &PublisherRuntime{}
	if r.CaptureDisabled() {
		t.Fatal("missing policy is not an explicit disable")
	}
	r.hasPolicy = true
	r.snapshot.EvidenceAdmission = "enabled"
	r.admitting = false
	if r.CaptureDisabled() {
		t.Fatal("temporary infrastructure failure is not policy disable")
	}
	r.snapshot.EvidenceAdmission = "disabled"
	if !r.CaptureDisabled() {
		t.Fatal("verified disable must stop lifecycle and artifact admission")
	}
	r.snapshot.EvidenceAdmission = "enabled"
	if r.CaptureDisabled() {
		t.Fatal("reenabled snapshot must clear intentional disable")
	}
}
