package bkntrace

import (
	"encoding/json"
	"strings"
)

const traceGapMarker = "\n[BKN_TRACE_GAP]"

// ExtractTraceGapMarkers removes only well-formed bounded trace-gap warnings.
// Other stderr remains byte-for-byte unchanged.
func ExtractTraceGapMarkers(stderr string) (string, []string) {
	var cleaned strings.Builder
	var reasons []string
	copied, scanned := 0, 0
	for len(reasons) < 64 {
		relative := strings.Index(stderr[scanned:], traceGapMarker)
		if relative < 0 {
			break
		}
		start := scanned + relative
		bodyStart := start + len(traceGapMarker)
		newline := strings.IndexByte(stderr[bodyStart:], '\n')
		if newline < 0 {
			break
		}
		end := bodyStart + newline
		scanned = end + 1
		if end-bodyStart > 512 {
			continue
		}
		var warning struct {
			PartialReason string `json:"partial_reason"`
		}
		if json.Unmarshal([]byte(stderr[bodyStart:end]), &warning) != nil || !IsTracePartialReason(warning.PartialReason) {
			continue
		}
		cleaned.WriteString(stderr[copied:start])
		copied = scanned
		reasons = append(reasons, warning.PartialReason)
	}
	if len(reasons) == 0 {
		return stderr, nil
	}
	cleaned.WriteString(stderr[copied:])
	return cleaned.String(), reasons
}
