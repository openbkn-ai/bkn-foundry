// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// Keep the numeric value for existing MCP callers and add its exact decimal
// spelling for UI hosts, which may otherwise round JSON numbers to float64.
type metricDisplayEntry struct {
	interfaces.ObjectMetricDataEntry
	DisplayValue string `json:"display_value,omitempty"`
}

type metricDisplayResult struct {
	*interfaces.QueryMetricResp
	Entries []metricDisplayEntry `json:"entries"`
}

func metricResultWithExactValues(resp *interfaces.QueryMetricResp) any {
	if resp == nil {
		return nil
	}
	entries := make([]metricDisplayEntry, 0, len(resp.Entries))
	for _, entry := range resp.Entries {
		display := metricDisplayEntry{ObjectMetricDataEntry: entry}
		switch value := entry.Value.(type) {
		case json.Number:
			display.DisplayValue = value.String()
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			display.DisplayValue = fmt.Sprint(value)
		}
		entries = append(entries, display)
	}
	return &metricDisplayResult{QueryMetricResp: resp, Entries: entries}
}
