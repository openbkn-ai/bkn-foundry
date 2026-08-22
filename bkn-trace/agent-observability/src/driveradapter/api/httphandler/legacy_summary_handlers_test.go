// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"net/http"
	"strings"
)

// registerLegacySummaryTestRoutes retains focused coverage for the former
// handler behavior without restoring any runtime route registration.
func registerLegacySummaryTestRoutes(mux *http.ServeMux, basePath string, handler *EvidenceHandler) {
	mux.HandleFunc(basePath+"/business-provenance/conversations", handler.ListBusinessProvenanceConversations)
	mux.HandleFunc(basePath+"/business-provenance/interactions", handler.ListBusinessProvenanceInteractions)
	mux.HandleFunc(basePath+"/business-provenance/interactions/", handler.GetInteractionSummary)
	mux.HandleFunc(basePath+"/business-provenance/traces/", handler.GetTraceSubresource)
	mux.HandleFunc(basePath+"/business-provenance/evidence-nodes/", handler.GetEvidenceNode)
	mux.HandleFunc(basePath+"/business-provenance/requests", handler.ListRequests)
	mux.HandleFunc(basePath+"/business-provenance/requests/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		switch {
		case strings.HasSuffix(path, "/traces"):
			handler.ListRequestTraces(w, r)
		case strings.HasSuffix(path, "/evidence-chain"):
			handler.GetEvidenceChainByRequestID(w, r)
		case strings.HasSuffix(path, "/business-graph"):
			handler.GetBusinessGraphByRequestID(w, r)
		case strings.HasSuffix(path, "/snapshot-preview"):
			handler.GetSnapshotPreviewByRequestID(w, r)
		default:
			handler.GetRequestSummary(w, r)
		}
	})
}
