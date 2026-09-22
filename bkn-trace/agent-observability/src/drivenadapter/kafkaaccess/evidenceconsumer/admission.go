package evidenceconsumer

import (
	"strconv"
	"strings"
	"time"
)

func AdmitLive(record Record, policy PolicySnapshot) Decision {
	if record.TimestampType != "LogAppendTime" {
		return Decision{Decision: "reject", Reason: "timestamp_type_invalid"}
	}
	if err := ValidateLiveRecordContract(record); err != nil {
		return Decision{Decision: "reject", Reason: "header_contract_invalid"}
	}
	headers, err := ParseHeaders(record.Headers)
	if err != nil {
		return Decision{Decision: "reject", Reason: "header_contract_invalid"}
	}
	revisionText := headers["capture_policy_revision"]
	if revisionText == "" || (len(revisionText) > 1 && revisionText[0] == '0') {
		return Decision{Decision: "reject", Reason: "capture_policy_revision_invalid"}
	}
	revision, err := strconv.ParseUint(revisionText, 10, 64)
	if err != nil || revision == 0 {
		return Decision{Decision: "reject", Reason: "capture_policy_revision_invalid"}
	}
	if revision != policy.Revision {
		return Decision{Decision: "reject", Reason: "capture_policy_revision_unknown"}
	}
	if !policy.Enabled {
		return Decision{Decision: "reject", Reason: "capture_policy_revision_disabled"}
	}
	instanceID := headers["producer_instance_id"]
	if instanceID == "" || policy.InstanceID != instanceID || policy.RegisteredRevision != revision {
		return Decision{Decision: "reject", Reason: "producer_instance_unknown"}
	}
	streamBoot := streamBootID(record.ProducerStreamID)
	instanceBoot := instanceBootID(instanceID)
	if streamBoot == "" || streamBoot != instanceBoot {
		return Decision{Decision: "reject", Reason: "producer_instance_stream_mismatch"}
	}
	closure := policy.Closure
	if closure.InstanceID == instanceID && closure.Revision == revision {
		if record.ProducerSequence > closure.LastAcceptedSequence {
			return Decision{Decision: "reject", Reason: "producer_sequence_after_closure"}
		}
		brokerTime, parseBrokerErr := time.Parse(time.RFC3339Nano, record.BrokerTimestamp)
		closedTime, parseClosedErr := time.Parse(time.RFC3339Nano, closure.ClosedAt)
		if parseBrokerErr != nil || parseClosedErr != nil {
			return Decision{Decision: "reject", Reason: "broker_timestamp_invalid"}
		}
		if brokerTime.After(closedTime) {
			return Decision{Decision: "reject", Reason: "record_appended_after_closure"}
		}
	}
	return Decision{Decision: "accept", Reason: "accepted_live"}
}

func streamBootID(stream string) string {
	parts := strings.Split(stream, ":")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}

func instanceBootID(instance string) string {
	parts := strings.Split(instance, "#")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}
