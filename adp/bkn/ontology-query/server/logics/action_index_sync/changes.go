// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_index_sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
)

// Prepare records only explicit committed changes. Object identities are never
// interpreted as source primary keys, and arbitrary nested outputs are not searched.
func Prepare(result *interfaces.ObjectExecutionResult, executor interfaces.AccountInfo) {
	if result.Status != interfaces.ObjectStatusSuccess || result.IndexSync != nil {
		return
	}
	body, ok := result.Result.(map[string]any)
	if !ok {
		return
	}
	if failed, _ := body["isError"].(bool); failed {
		return
	}
	if structured, ok := body["structuredContent"].(map[string]any); ok {
		body = structured
	}
	if failed, _ := body["isError"].(bool); failed {
		return
	}
	value, declared := body["data_changes"]
	if !declared {
		return
	}
	sync := &interfaces.ActionIndexSync{Status: "invalid", Executor: executor, Error: "invalid committed data_changes declaration"}
	result.IndexSync = sync
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > 4*1024*1024 {
		return
	}
	var changes struct {
		Committed bool `json:"committed"`
		Resources []struct {
			ResourceID string           `json:"resource_id"`
			Keys       []map[string]any `json:"keys"`
		} `json:"affected_resources"`
	}
	if jsoncodec.Unmarshal(encoded, &changes) != nil || !changes.Committed || len(changes.Resources) == 0 || len(changes.Resources) > 16 {
		return
	}
	total := 0
	for _, resource := range changes.Resources {
		if resource.ResourceID == "" || resource.ResourceID != strings.TrimSpace(resource.ResourceID) || len(resource.ResourceID) > 128 || len(resource.Keys) == 0 {
			return
		}
		total += len(resource.Keys)
		if total > 10000 {
			return
		}
		for _, key := range resource.Keys {
			if len(key) == 0 || len(key) > 32 {
				return
			}
			for field, value := range key {
				if field == "" {
					return
				}
				switch value.(type) {
				case string, json.Number:
				default:
					return
				}
			}
		}
		for start := 0; start < len(resource.Keys); start += 100 {
			end := min(start+100, len(resource.Keys))
			sync.Tasks = append(sync.Tasks, interfaces.ActionIndexSyncTask{ResourceID: resource.ResourceID, Keys: resource.Keys[start:end]})
		}
	}
	if executor.ID == "" || executor.Type == "" {
		sync.Error = "action initiator is unavailable"
		return
	}
	sync.Status = "pending"
	sync.Error = ""
}

// PrepareFunction accepts the explicit JSON stdout contract only for successful
// Function invocations. A nonzero exit code is rejected by the action executor.
func PrepareFunction(result *interfaces.ObjectExecutionResult, executor interfaces.AccountInfo) {
	if result.Status != interfaces.ObjectStatusSuccess {
		return
	}
	body, ok := result.Result.(map[string]any)
	if !ok {
		return
	}
	stdout, ok := body["stdout"].(string)
	if !ok || len(stdout) > 4*1024*1024 {
		return
	}
	var output map[string]any
	if jsoncodec.Unmarshal([]byte(stdout), &output) != nil {
		return
	}
	copy := *result
	copy.Result = output
	Prepare(&copy, executor)
	result.IndexSync = copy.IndexSync
}

func syncError(operation string, err error) string {
	// Downstream bodies may contain resource or platform details. Persist a stable
	// operation label; error details belong to the protected downstream task.
	var downstream *interfaces.VegaDownstreamError
	if errors.As(err, &downstream) {
		return fmt.Sprintf("%s failed (HTTP %d)", operation, downstream.StatusCode)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return operation + " timed out"
	}
	return fmt.Sprintf("%s failed", operation)
}
