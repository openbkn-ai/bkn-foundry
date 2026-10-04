// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import (
	"errors"
	"slices"
	"strings"
)

const MaxTraceCallGapReasons = 64

// ParseTraceCallGap accepts only current registered Context Loader tool names.
// A gap describes missing recording, never a forged operation or receipt ID.
func ParseTraceCallGap(reason string) (tool, requestID string, ok bool) {
	parts := strings.SplitN(reason, ":", 3)
	if len(parts) != 3 || parts[0] != "trace_call_unrecorded" {
		return "", "", false
	}
	switch parts[1] {
	case "bkn_finish_interaction", "bkn_start_interaction", "describe_native_tool", "describe_resource", "execute_action", "execute_native_tool", "execute_skill", "execute_tool", "explore_subgraph", "get_action_execution", "get_action_info", "get_kn_detail", "get_logic_properties_values", "get_object_types", "get_relation_types", "get_skill_content", "list_action_executions", "list_knowledge_networks", "list_resources", "list_skills", "query_instance_subgraph", "query_metric", "query_object_instance", "read_skill_file", "run_code", "run_cypher", "run_shell", "run_sql", "search_capabilities", "search_instance", "search_native_tools", "search_schema":
	default:
		return "", "", false
	}
	if len(parts[2]) == 0 || len(parts[2]) > 128 {
		return "", "", false
	}
	for _, ch := range parts[2] {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == ':' || ch == '-' {
			continue
		}
		return "", "", false
	}
	return parts[1], parts[2], true
}
func NormalizeTraceCallGaps(reasons []string) ([]string, error) {
	if len(reasons) > MaxTraceCallGapReasons {
		return nil, errors.New("too many Trace call gap reasons")
	}
	result := append(make([]string, 0, len(reasons)), reasons...)
	for _, reason := range result {
		if _, _, ok := ParseTraceCallGap(reason); !ok {
			return nil, errors.New("invalid Trace call gap reason")
		}
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}
