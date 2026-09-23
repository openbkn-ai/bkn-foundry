package metric

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

func TestAuditLogBuilderNoLongerPublishesLegacyTopic(t *testing.T) {
	source, err := os.ReadFile("audit_log.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"AuditLogTopic", "OutboxMessageReq", "outboxMessageEvent"} {
		if strings.Contains(string(source), legacy) {
			t.Errorf("AuditLogBuilder still references legacy outbox path %q", legacy)
		}
	}
}

type captureAuditPublisher struct {
	value       []byte
	disposition auditpublisher.Disposition
}

func (c *captureAuditPublisher) TryPublish(value []byte) auditpublisher.Disposition {
	c.value = append([]byte(nil), value...)
	return c.disposition
}

type auditTestLogger struct{}

func (auditTestLogger) Debug(...interface{})                            {}
func (auditTestLogger) Info(...interface{})                             {}
func (auditTestLogger) Warn(...interface{})                             {}
func (auditTestLogger) Error(...interface{})                            {}
func (auditTestLogger) Debugf(string, ...interface{})                   {}
func (auditTestLogger) Infof(string, ...interface{})                    {}
func (auditTestLogger) Warnf(string, ...interface{})                    {}
func (auditTestLogger) Errorf(string, ...interface{})                   {}
func (l auditTestLogger) WithContext(context.Context) interfaces.Logger { return l }

func TestAuditLogBuilderSerializesOnlyControlledAuditFields(t *testing.T) {
	t.Setenv("BKN_AUDIT_ENVIRONMENT", "test")
	publisher := &captureAuditPublisher{disposition: auditpublisher.Accepted}
	builder := &AuditLogBuilder{logger: auditTestLogger{}, publisher: publisher, env: "test"}
	builder.Logger(context.Background(), &AuditLogBuilderParams{
		TokenInfo:   &interfaces.TokenInfo{VisitorID: "user-1", VisitorTyp: interfaces.RealName},
		Accessor:    &interfaces.AuthAccessor{ID: "user-1", Name: "Alice"},
		Operation:   AuditLogOperationExecute,
		Object:      &AuditLogObject{Type: AuditLogObjectTool, ID: "box-1", Name: "tool name is not a toolbox name"},
		Description: "untrusted arbitrary description",
		ExMsg:       "private exception detail",
		Detils:      map[string]any{"request_body": "sensitive"},
	})
	if len(publisher.value) == 0 {
		t.Fatal("expected canonical audit payload to be published")
	}
	var payload map[string]any
	if err := json.Unmarshal(publisher.value, &payload); err != nil {
		t.Fatal(err)
	}
	record, err := auditpublisher.BuildRecord(publisher.value)
	if err != nil {
		t.Fatalf("payload violates the frozen Audit v1 producer contract: %v", err)
	}
	if record.Topic != auditpublisher.Topic || string(record.Key) != "execution-factory\x1ftoolbox\x1fbox-1" || record.TimestampType != auditpublisher.LogAppendTime || len(record.Headers) != 1 || record.Headers[0].Key != auditpublisher.SchemaVersionHeader || string(record.Headers[0].Value) != auditpublisher.SchemaVersion {
		t.Fatalf("unexpected frozen Kafka record: %#v", record)
	}
	if !strings.HasPrefix(record.ContentHash, "sha256:") {
		t.Fatalf("missing canonical payload hash: %q", record.ContentHash)
	}
	if payload["event_name"] != "execution_factory.operation.observed" {
		t.Fatalf("legacy audit payload has event_name=%v", payload["event_name"])
	}
	if payload["source_id"] != "execution-factory" || payload["category"] != "audit.admin" {
		t.Fatalf("unexpected event identity: %#v", payload)
	}
	if payload["outcome"] != "success" {
		t.Fatalf("outcome=%v, want success", payload["outcome"])
	}
	if payload["summary"] != "execution_factory.operation.observed execute toolbox" {
		t.Fatalf("summary=%v", payload["summary"])
	}
	actor, _ := payload["actor"].(map[string]any)
	if actor["id"] != "user-1" || actor["effective_subject"] != "user-1" || actor["type"] != "user" || actor["auth_method"] != "unknown" {
		t.Fatalf("actor mapping=%#v", actor)
	}
	target, _ := payload["target"].(map[string]any)
	if target["type"] != "toolbox" || target["id"] != "box-1" {
		t.Fatalf("target mapping=%#v", target)
	}
	if _, exists := target["name"]; exists {
		t.Fatalf("legacy free-form object name leaked into target: %#v", target)
	}
	facts, _ := payload["facts"].(map[string]any)
	if len(facts) != 1 || facts["action"] != "execute" {
		t.Fatalf("facts=%#v", facts)
	}
	if _, exists := payload["http_status"]; exists {
		t.Fatalf("unknown final HTTP status must not be guessed: %#v", payload)
	}
	for _, forbidden := range []string{"description", "ex_msg", "detail", "changed_fields", "before_hash", "after_hash", "content_hash"} {
		if _, exists := payload[forbidden]; exists {
			t.Errorf("forbidden legacy value %q leaked: %#v", forbidden, payload)
		}
	}
}

func TestAuditLogBuilderFailsOpenWhenPublisherDrops(t *testing.T) {
	publisher := &captureAuditPublisher{disposition: auditpublisher.DroppedQueueFull}
	builder := &AuditLogBuilder{logger: auditTestLogger{}, publisher: publisher, env: "test"}
	builder.Logger(context.Background(), &AuditLogBuilderParams{
		TokenInfo: &interfaces.TokenInfo{VisitorID: "user-1", VisitorTyp: interfaces.RealName},
		Accessor:  &interfaces.AuthAccessor{ID: "user-1", Name: "Alice"},
		Operation: AuditLogOperationExecute,
		Object:    &AuditLogObject{Type: AuditLogObjectMCP, ID: "mcp-1"},
	})
	if len(publisher.value) == 0 {
		t.Fatal("expected an audit attempt even when the publisher drops it")
	}
}

func TestAuditLogBuilderKeepsMCPToolResultFailure(t *testing.T) {
	publisher := &captureAuditPublisher{disposition: auditpublisher.Accepted}
	builder := &AuditLogBuilder{logger: auditTestLogger{}, publisher: publisher, env: "test"}
	builder.Logger(context.Background(), &AuditLogBuilderParams{
		TokenInfo: &interfaces.TokenInfo{VisitorID: "user-1", VisitorTyp: interfaces.RealName},
		Accessor:  &interfaces.AuthAccessor{ID: "user-1", Name: "Alice"},
		Operation: AuditLogOperationExecute, Outcome: "failure", FailureCode: "TOOL_EXECUTION_FAILED",
		Object: &AuditLogObject{Type: AuditLogObjectMCP, ID: "mcp-1"},
	})
	var payload map[string]any
	if err := json.Unmarshal(publisher.value, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["outcome"] != "failure" || payload["failure_code"] != "TOOL_EXECUTION_FAILED" {
		t.Fatalf("MCP tool result failure was lost: %#v", payload)
	}
}

func TestAuditLogBuilderUsesFrozenActorFallbacks(t *testing.T) {
	tests := []struct {
		name, visitorID, accessorID string
		visitorType                 interfaces.VisitorType
		want                        string
	}{
		{name: "verified token identity wins", visitorID: "token-user", accessorID: "accessor-user", visitorType: interfaces.RealName, want: "token-user"},
		{name: "accessor identity fallback", accessorID: "accessor-user", visitorType: interfaces.RealName, want: "accessor-user"},
		{name: "anonymous sentinel", visitorType: interfaces.Anonymous, want: "anonymous"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			publisher := &captureAuditPublisher{disposition: auditpublisher.Accepted}
			builder := &AuditLogBuilder{logger: auditTestLogger{}, publisher: publisher, env: "test"}
			builder.Logger(context.Background(), &AuditLogBuilderParams{
				TokenInfo: &interfaces.TokenInfo{VisitorID: tc.visitorID, VisitorTyp: tc.visitorType},
				Accessor:  &interfaces.AuthAccessor{ID: tc.accessorID}, Operation: AuditLogOperationExecute,
				Object: &AuditLogObject{Type: AuditLogObjectOperator, ID: "operator-1"},
			})
			var payload map[string]any
			if err := json.Unmarshal(publisher.value, &payload); err != nil {
				t.Fatal(err)
			}
			actor := payload["actor"].(map[string]any)
			if actor["id"] != tc.want || actor["effective_subject"] != tc.want {
				t.Fatalf("actor fallback=%#v, want id/effective_subject %q", actor, tc.want)
			}
		})
	}
}

func TestAuditLogBuilderDropsInvalidEnvironment(t *testing.T) {
	publisher := &captureAuditPublisher{disposition: auditpublisher.Accepted}
	builder := &AuditLogBuilder{logger: auditTestLogger{}, publisher: publisher, env: "foo"}
	builder.Logger(context.Background(), &AuditLogBuilderParams{
		TokenInfo: &interfaces.TokenInfo{VisitorID: "user-1", VisitorTyp: interfaces.RealName},
		Accessor:  &interfaces.AuthAccessor{ID: "user-1"}, Operation: AuditLogOperationExecute,
		Object: &AuditLogObject{Type: AuditLogObjectOperator, ID: "operator-1"},
	})
	if len(publisher.value) != 0 {
		t.Fatalf("invalid environment was published: %s", publisher.value)
	}
}
