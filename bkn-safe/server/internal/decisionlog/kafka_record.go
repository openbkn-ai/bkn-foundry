package decisionlog

import "strings"

// safeReference keeps normal OpenBKN identifiers, including bkn_ prefixes,
// while omitting caller-supplied credential strings from Safe's Audit facts.
func safeReference(raw string) string {
	id := strings.TrimSpace(raw)
	if id == "" {
		return ""
	}
	if len(id) > 256 || strings.ContainsAny(id, " \t\r\n") {
		return "redacted"
	}
	parts := strings.Split(id, "_")
	if len(parts) == 3 && parts[0] == "bak" && len(parts[1]) == 12 && len(parts[2]) == 27 {
		return "redacted"
	}
	return id
}

// IsSafeActorID reports whether an identity can be retained in an audit actor
// field. Caller-supplied credential shapes are redacted instead of emitted.
func IsSafeActorID(raw string) bool {
	id := strings.TrimSpace(raw)
	return id != "" && id != "redacted" && safeReference(raw) == id
}
