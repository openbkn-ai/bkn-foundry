package evidencepublisher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const traceEvidenceControlPath = "/api/agent-observability/v1/internal/trace-evidence"

var errInvalidControlClient = errors.New("invalid evidence publisher control client")
var errPublisherAcknowledgementNotExpected = errors.New("publisher acknowledgement instance is outside the frozen expected set")

// ControlClientConfig configures internal heartbeat and publisher-ack calls.
type ControlClientConfig struct {
	BaseURL    string
	HTTPClient *http.Client
}

type ControlClient struct {
	baseURL string
	client  *http.Client
}

func NewControlClient(config ControlClientConfig) (*ControlClient, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errInvalidControlClient
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &ControlClient{baseURL: strings.TrimRight(baseURL, "/") + traceEvidenceControlPath, client: client}, nil
}

// Heartbeat reports this workload's identity and observed policy revision.
func (c *ControlClient) Heartbeat(ctx context.Context, workloadIdentity, processBootID string, revision uint64) error {
	workloadIdentity = strings.TrimSpace(workloadIdentity)
	processBootID = strings.TrimSpace(processBootID)
	if !c.valid() || workloadIdentity == "" || processBootID == "" || revision == 0 {
		return errInvalidControlClient
	}
	body, err := json.Marshal(struct {
		EndpointKind     string `json:"endpoint_kind"`
		WorkloadIdentity string `json:"workload_identity"`
		InstanceID       string `json:"instance_id"`
		ProcessBootID    string `json:"process_boot_id"`
		ObservedRevision uint64 `json:"observed_revision"`
		Ready            bool   `json:"ready"`
	}{"evidence_publisher", workloadIdentity, workloadIdentity + "#" + processBootID, processBootID, revision, true})
	if err != nil {
		return fmt.Errorf("encode trace evidence heartbeat: %w", err)
	}
	return c.post(ctx, c.baseURL+"/endpoints:heartbeat", body, "heartbeat")
}

// Acknowledge reports a drain only after ConfigurationClient has supplied an
// active operation candidate matching the validated policy revision. S3 still
// atomically rechecks active phase and revision before recording it.
func (c *ControlClient) Acknowledge(ctx context.Context, operation ConfigurationOperation, ack DrainResult, acknowledgedAt time.Time) error {
	if !c.valid() || strings.TrimSpace(operation.ID) == "" || operation.Revision == 0 || strings.TrimSpace(ack.ProducerInstanceID) == "" || !ack.QueueEmpty || acknowledgedAt.IsZero() || ack.Published+ack.Dropped != ack.LastAcceptedSequence {
		return errInvalidControlClient
	}
	revision, err := parseCanonicalRevision(ack.CapturePolicyRevision)
	if err != nil || revision != operation.Revision {
		return errInvalidControlClient
	}
	body, err := json.Marshal(struct {
		ProducerInstanceID    string `json:"producer_instance_id"`
		CapturePolicyRevision uint64 `json:"capture_policy_revision"`
		LastAcceptedSequence  uint64 `json:"last_accepted_sequence"`
		Published             uint64 `json:"published"`
		Dropped               uint64 `json:"dropped"`
		QueueEmpty            bool   `json:"queue_empty"`
		AcknowledgedAt        string `json:"acknowledged_at"`
	}{ack.ProducerInstanceID, revision, ack.LastAcceptedSequence, ack.Published, ack.Dropped, ack.QueueEmpty, formatAcknowledgedAt(acknowledgedAt)})
	if err != nil {
		return fmt.Errorf("encode trace evidence publisher acknowledgement: %w", err)
	}
	return c.post(ctx, c.baseURL+"/operations/"+url.PathEscape(operation.ID)+":publisher-ack", body, "publisher acknowledgement")
}

func formatAcknowledgedAt(value time.Time) string {
	return value.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z07:00")
}

func (c *ControlClient) valid() bool { return c != nil && c.client != nil }

func (c *ControlClient) post(ctx context.Context, endpoint string, body []byte, operation string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create trace evidence %s request: %w", operation, err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("send trace evidence %s: %w", operation, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if operation == "publisher acknowledgement" && response.StatusCode == http.StatusConflict {
			var detail struct {
				Code string `json:"code"`
			}
			if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&detail) == nil && detail.Code == "EVIDENCE_PUBLISHER_ACK_NOT_EXPECTED" {
				return fmt.Errorf("send trace evidence %s: unexpected status %d: %w", operation, response.StatusCode, errPublisherAcknowledgementNotExpected)
			}
		}
		return fmt.Errorf("send trace evidence %s: unexpected status %d", operation, response.StatusCode)
	}
	return nil
}

func parseCanonicalRevision(value string) (uint64, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, errors.New("revision is not canonical")
	}
	revision, err := strconv.ParseUint(value, 10, 64)
	if err != nil || revision == 0 {
		return 0, errors.New("revision is invalid")
	}
	return revision, nil
}
