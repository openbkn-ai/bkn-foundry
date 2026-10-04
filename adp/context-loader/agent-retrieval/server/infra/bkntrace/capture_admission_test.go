package bkntrace

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
	"testing"
)

type disabledCapturePublisher struct{}

func (disabledCapturePublisher) CaptureDisabled() bool { return true }
func (disabledCapturePublisher) TryPublish(evidencepublisher.Event) evidencepublisher.PublishResult {
	panic("disabled capture must not publish")
}

func TestDisabledCaptureSkipsArtifactAndOperationAdmission(t *testing.T) {
	SetEvidencePublisher(disabledCapturePublisher{})
	t.Cleanup(func() { SetEvidencePublisher(nil) })
	t.Setenv("BKN_TRACE_ARTIFACT_ENDPOINT", "http://never-called.invalid")
	if ref, err := RecordInteractionArtifact(context.Background(), "conv", "int", InteractionArtifactResult, "answer"); err != nil || ref != "" {
		t.Fatalf("ref=%q err=%v", ref, err)
	}
	guard := NewGuard(nil)
	ctx, state, disposition, apiErr, err := guard.Begin(context.Background(), GuardIntent{})
	if err != nil || apiErr != nil || disposition != GuardExecute {
		t.Fatalf("disabled begin: %v %v %q", err, apiErr, disposition)
	}
	if _, apiErr, err := guard.Finish(ctx, state, map[string]any{"business": "ok"}, false, false); err != nil || apiErr != nil {
		t.Fatalf("disabled finish: %v %v", err, apiErr)
	}
	if reasons := TracePartialReasons(ctx); len(reasons) != 0 {
		t.Fatalf("policy disable is not a gap: %v", reasons)
	}
}

func TestDisabledCaptureSkipsPayloadArtifactAdmission(t *testing.T) {
	SetEvidencePublisher(disabledCapturePublisher{})
	t.Cleanup(func() { SetEvidencePublisher(nil) })
	_, _, err := (evidencePayloadArtifactWriter{}).Put(context.Background(), "application/json", []byte(`{"result":"business"}`))
	coreErr, ok := err.(*CoreHTTPError)
	if !ok || coreErr.Code != "CAPTURE_DISABLED" {
		t.Fatalf("disabled payload did not return intentional policy result: %v", err)
	}
}
