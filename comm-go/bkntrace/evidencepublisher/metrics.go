package evidencepublisher

// Metrics is intentionally a low-cardinality hook. Integrations may implement
// it without exposing event, trace, interaction, owner, or business IDs.
type Metrics interface {
	Eligible()
	Accepted()
	Published()
	Dropped(reason string)
}

type noopMetrics struct{}

func (noopMetrics) Eligible()      {}
func (noopMetrics) Accepted()      {}
func (noopMetrics) Published()     {}
func (noopMetrics) Dropped(string) {}
