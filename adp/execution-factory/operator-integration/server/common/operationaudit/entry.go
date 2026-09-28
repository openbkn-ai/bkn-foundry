// Package operationaudit emits bounded management-attempt facts through the
// independent Kafka Audit pipeline. Historical SQL records remain untouched.
package operationaudit

import "time"

type Entry struct {
	EventID        string    `json:"event_id"`
	EventTime      time.Time `json:"event_time"`
	RecordedAt     time.Time `json:"recorded_at"`
	ActorID        string    `json:"actor_id"`
	ActorName      string    `json:"actor_name"`
	ActorType      string    `json:"actor_type"`
	AuthMethod     string    `json:"auth_method"`
	RequestID      string    `json:"request_id"`
	SourceChannel  string    `json:"source_channel"`
	Method         string    `json:"method"`
	HTTPStatus     int       `json:"http_status"`
	Action         string    `json:"action"`
	TargetType     string    `json:"target_type"`
	TargetID       string    `json:"target_id"`
	TargetName     string    `json:"target_name"`
	Outcome        string    `json:"outcome"`
	FailureCode    string    `json:"failure_code"`
	FailureMessage string    `json:"failure_message"`
}
