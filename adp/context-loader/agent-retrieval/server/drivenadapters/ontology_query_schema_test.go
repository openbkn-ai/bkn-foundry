// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.

package drivenadapters

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type recordingLogger struct {
	entries []string
}

func (l *recordingLogger) append(values ...interface{}) {
	l.entries = append(l.entries, fmt.Sprint(values...))
}
func (l *recordingLogger) appendf(format string, values ...interface{}) {
	l.entries = append(l.entries, fmt.Sprintf(format, values...))
}
func (l *recordingLogger) Debug(values ...interface{})                         { l.append(values...) }
func (l *recordingLogger) Debugf(format string, values ...interface{})         { l.appendf(format, values...) }
func (l *recordingLogger) Info(values ...interface{})                          { l.append(values...) }
func (l *recordingLogger) Infof(format string, values ...interface{})          { l.appendf(format, values...) }
func (l *recordingLogger) Warn(values ...interface{})                          { l.append(values...) }
func (l *recordingLogger) Warnf(format string, values ...interface{})          { l.appendf(format, values...) }
func (l *recordingLogger) Error(values ...interface{})                         { l.append(values...) }
func (l *recordingLogger) Errorf(format string, values ...interface{})         { l.appendf(format, values...) }
func (l *recordingLogger) WithContext(context.Context) interfaces.Logger       { return l }
func (l *recordingLogger) WithField(string, interface{}) interfaces.Logger     { return l }
func (l *recordingLogger) WithFields(map[string]interface{}) interfaces.Logger { return l }

func TestGetObjectTypeSchemaForwardsIdentityAndPermissions(t *testing.T) {
	client := &ontologyQueryClient{
		logger:  &mockLogger{},
		baseURL: "http://ontology-query/api/ontology-query",
		httpClient: &mockHTTPClient{bytesFunc: func(_ context.Context, method, target string,
			header map[string]string, _ interface{}) (int, []byte, error) {
			if method != "GET" || !strings.HasSuffix(target, "/in/v1/knowledge-networks/kn%2F1/object-types/customer%3F/schema") {
				t.Fatalf("unexpected request: %s %s", method, target)
			}
			if header["x-account-id"] != "account-1" || header["x-account-type"] != "user" {
				t.Fatalf("caller identity headers = %#v", header)
			}
			return 200, []byte(`{"schema_definition":[{"name":"phone"}],"effective_permissions":{"phone":"masked"}}`), nil
		}},
	}
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "account-1", AccountType: interfaces.AccessorTypeUser,
	})

	resp, err := client.GetObjectTypeSchema(ctx, "kn/1", "customer?")
	if err != nil {
		t.Fatal(err)
	}
	if resp.EffectivePermissions["phone"] != interfaces.PropertyAccessMasked {
		t.Fatalf("permissions = %#v", resp.EffectivePermissions)
	}
}

func TestObjectAndLogicResponsesPreserveEffectivePermissions(t *testing.T) {
	responses := [][]byte{
		[]byte(`{"datas":[],"effective_permissions":{"phone":"masked"}}`),
		[]byte(`{"datas":[],"effective_permissions":{"amount":"full"}}`),
	}
	client := &ontologyQueryClient{
		logger: &mockLogger{}, baseURL: "http://ontology-query",
		httpClient: &mockHTTPClient{bytesFunc: func(context.Context, string, string, map[string]string, interface{}) (int, []byte, error) {
			body := responses[0]
			responses = responses[1:]
			return 200, body, nil
		}},
	}
	objectResp, err := client.QueryObjectInstances(context.Background(), &interfaces.QueryObjectInstancesReq{KnID: "kn", OtID: "ot"})
	if err != nil {
		t.Fatal(err)
	}
	logicResp, err := client.QueryLogicProperties(context.Background(), &interfaces.QueryLogicPropertiesReq{KnID: "kn", OtID: "ot"})
	if err != nil {
		t.Fatal(err)
	}
	if objectResp.EffectivePermissions["phone"] != interfaces.PropertyAccessMasked ||
		logicResp.EffectivePermissions["amount"] != interfaces.PropertyAccessFull {
		t.Fatalf("effective permissions were dropped: object=%#v logic=%#v", objectResp, logicResp)
	}
}

func TestObjectQueryLogsDoNotContainRequestOrResponseValues(t *testing.T) {
	logger := &recordingLogger{}
	client := &ontologyQueryClient{
		logger: logger, baseURL: "http://ontology-query",
		httpClient: &mockHTTPClient{bytesFunc: func(context.Context, string, string, map[string]string, interface{}) (int, []byte, error) {
			return 200, []byte(`{"datas":[{"phone":"response-secret"}]}`), nil
		}},
	}
	_, err := client.QueryLogicProperties(context.Background(), &interfaces.QueryLogicPropertiesReq{
		KnID: "kn", OtID: "ot", InstanceIdentities: []map[string]interface{}{{"id": "request-secret"}},
		Properties: []string{"phone"}, DynamicParams: map[string]interface{}{"query": "request-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	logs := strings.Join(logger.entries, "\n")
	for _, secret := range []string{"request-secret", "response-secret"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("protected value %q leaked into logs: %s", secret, logs)
		}
	}
}
