package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

type flushCaptureSender struct{ records chan evidencepublisher.Record }

func (s flushCaptureSender) Send(_ context.Context, record evidencepublisher.Record) error {
	s.records <- record
	return nil
}

func TestRouteExtensionsFreezeInStableOrder(t *testing.T) {
	application := &Application{routeExtensions: make(map[string]func(*gin.Engine))}
	installed := make([]string, 0, 2)
	if err := application.RegisterRoutes("zeta", func(*gin.Engine) { installed = append(installed, "zeta") }); err != nil {
		t.Fatal(err)
	}
	if err := application.RegisterRoutes("alpha", func(*gin.Engine) { installed = append(installed, "alpha") }); err != nil {
		t.Fatal(err)
	}
	for _, install := range application.freezeRouteExtensions() {
		install(nil)
	}
	if want := []string{"alpha", "zeta"}; !reflect.DeepEqual(installed, want) {
		t.Fatalf("install order = %v, want %v", installed, want)
	}
	if err := application.RegisterRoutes("late", func(*gin.Engine) {}); err == nil {
		t.Fatal("RegisterRoutes after freeze succeeded")
	}
}

func TestRouteExtensionsRejectInvalidAndDuplicateRegistrations(t *testing.T) {
	application := &Application{routeExtensions: make(map[string]func(*gin.Engine))}
	if err := application.RegisterRoutes("", func(*gin.Engine) {}); err == nil {
		t.Fatal("RegisterRoutes accepted an empty name")
	}
	if err := application.RegisterRoutes("metrics", nil); err == nil {
		t.Fatal("RegisterRoutes accepted a nil installer")
	}
	if err := application.RegisterRoutes("metrics", func(*gin.Engine) {}); err != nil {
		t.Fatal(err)
	}
	if err := application.RegisterRoutes("metrics", func(*gin.Engine) {}); err == nil {
		t.Fatal("RegisterRoutes accepted a duplicate name")
	}
}

func TestEvidenceFlushLoopDeliversQueuedRecord(t *testing.T) {
	records := make(chan evidencepublisher.Record, 1)
	publisher, err := evidencepublisher.New(evidencepublisher.Config{
		ProducerID: "bkn-backend", BaseStreamID: "bkn-backend", WorkloadIdentity: "bkn-backend",
		ProcessBootID: "test-boot", CapturePolicyRevision: "1",
	}, flushCaptureSender{records: records})
	if err != nil {
		t.Fatal(err)
	}
	if publisher.TryPublish(evidencepublisher.Event{EventID: "evt-flush", EventType: "test.event", Envelope: []byte(`{"ok":true}`)}).Disposition != evidencepublisher.Accepted {
		t.Fatal("event was not accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := startEvidenceFlushLoop(ctx, publisher, time.Millisecond)
	select {
	case <-records:
	case <-time.After(time.Second):
		t.Fatal("flush loop did not deliver queued record")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("flush loop did not stop")
	}
}
