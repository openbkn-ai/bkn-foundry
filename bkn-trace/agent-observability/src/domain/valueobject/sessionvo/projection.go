package sessionvo

type ProjectionMutation struct {
	EventID          string
	AggregateType    string
	AggregateID      string
	AggregateVersion uint64
	EventType        string
	Payload          []byte
}
