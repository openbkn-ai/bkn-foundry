// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// Exercise the real adapter serializer, not a duplicate request struct. Unknown
// execution fields must not be silently lost between the project and Agent.
func TestPinnedExecutionRoundTrip(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"task","agent_id":"extractor","agent_version":7,"prompt_version":4,"model":"registered-model","model_id":"provider-revision","status":"succeeded","output":"{}","create_time":1,"update_time":2}`))
	}))
	defer server.Close()
	client := &bknAgentAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL}
	var req interfaces.BknAgentRunRequest
	if err := json.Unmarshal([]byte(`{"agent_id":"extractor","agent_version":7,"message":"synthetic input","prompt_override":"confirmed template"}`), &req); err != nil {
		t.Fatal(err)
	}
	account := interfaces.BknAgentAccount{ID: "job-principal", Type: "app"}
	task, err := client.RunTask(context.Background(), account, &req)
	if err != nil {
		t.Fatal(err)
	}
	if request["agent_version"] != float64(7) || request["prompt_override"] != "confirmed template" {
		t.Errorf("execution pin or confirmed template dropped: %v", request)
	}
	polled, err := client.GetTask(context.Background(), account, task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []*interfaces.BknAgentTask{task, polled} {
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]any{"agent_version": float64(7), "prompt_version": float64(4), "model": "registered-model", "model_id": "provider-revision"} {
			if fields[key] != want {
				t.Errorf("execution metadata %s lost: got %v, want %v", key, fields[key], want)
			}
		}
	}
}

func TestLegacyTaskExecutionMetadataIsUnknown(t *testing.T) {
	for _, body := range []string{
		`{"task_id":"old","status":"succeeded"}`,
		`{"task_id":"old","agent_version":null,"prompt_version":null,"model":null,"model_id":null,"status":"succeeded"}`,
	} {
		task, err := readTask([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if task.AgentVersion != nil || task.PromptVersion != nil || task.Model != "" || task.ModelID != "" {
			t.Fatal("legacy execution metadata must remain unknown")
		}
	}
}
