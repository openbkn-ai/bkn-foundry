package httphandler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

const responseTraceIDHeader = "x-trace-id"

func ensureResponseTraceID(w http.ResponseWriter, r *http.Request) string {
	if traceID := validTraceID(r.Header.Get(responseTraceIDHeader)); traceID != "" {
		w.Header().Set(responseTraceIDHeader, traceID)
		return traceID
	}
	parts := strings.Split(strings.TrimSpace(r.Header.Get("traceparent")), "-")
	if len(parts) == 4 {
		if traceID := validTraceID(parts[1]); traceID != "" {
			w.Header().Set(responseTraceIDHeader, traceID)
			return traceID
		}
	}
	return ensureWriterTraceID(w)
}

func ensureWriterTraceID(w http.ResponseWriter) string {
	if traceID := validTraceID(w.Header().Get(responseTraceIDHeader)); traceID != "" {
		return traceID
	}
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		sum := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
		buffer = sum[:16]
	}
	traceID := hex.EncodeToString(buffer)
	w.Header().Set(responseTraceIDHeader, traceID)
	return traceID
}

func validTraceID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 32 || value == strings.Repeat("0", 32) {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}
