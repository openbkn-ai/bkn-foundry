package decisionlog

const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
	DecisionNone  = "none"
)

// Entry is an observed authorization result. AccessorID may be supplied by an
// internal caller; only VerifiedActorID identifies an authenticated actor.
type Entry struct {
	AccessorID        string
	VerifiedActorID   string
	ResourceType      string
	ResourceID        string
	Operation         string
	Scope             string
	Decision          string
	Basis             string
	DeniedRequirement string
	Source            string
	RequestID         string
	TraceID           string
	Method            string
	ClientIP          string
	Detail            string
}
