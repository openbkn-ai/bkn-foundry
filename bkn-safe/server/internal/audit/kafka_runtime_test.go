package audit

import (
	"context"
	"testing"
)

func TestKafkaRuntimeWithoutConfigKeepsBusinessFailOpen(t *testing.T) {
	runtime := NewKafkaRuntimeFromEnv(func(string) string { return "" })
	if runtime == nil || runtime.Recorder == nil {
		t.Fatal("missing configuration must still provide a fail-open recorder")
	}
	if err := runtime.Recorder.Record(context.Background(), Entry{
		RequestID: "req-safe-unconfigured", Method: "POST", Resource: "users", Action: "create", Status: 400,
	}); err == nil {
		t.Fatal("unconfigured publisher was not reported as a coverage gap")
	}
	runtime.Close()
}
