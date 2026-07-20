// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package observability

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type AuditEvent struct {
	Timestamp  string `json:"timestamp"`
	RequestID  string `json:"request_id"`
	UserID     string `json:"user_id"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Route      string `json:"route"`
	Status     int    `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

func WriteAuditEvent(event AuditEvent) {
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return
	}

	fmt.Fprintln(os.Stdout, string(payload))
}
