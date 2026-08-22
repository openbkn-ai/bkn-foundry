// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ledgervo

func HasCausationCycle(events []Event) bool {
	graph := make(map[string][]string, len(events))
	for _, event := range events {
		graph[event.EventID] = append([]string(nil), event.CausationEventIDs...)
	}
	visiting := make(map[string]bool, len(graph))
	visited := make(map[string]bool, len(graph))
	var visit func(string) bool
	visit = func(eventID string) bool {
		if visiting[eventID] {
			return true
		}
		if visited[eventID] {
			return false
		}
		causes, exists := graph[eventID]
		if !exists {
			return false
		}
		visiting[eventID] = true
		for _, causeID := range causes {
			if visit(causeID) {
				return true
			}
		}
		visiting[eventID] = false
		visited[eventID] = true
		return false
	}
	for eventID := range graph {
		if visit(eventID) {
			return true
		}
	}
	return false
}
