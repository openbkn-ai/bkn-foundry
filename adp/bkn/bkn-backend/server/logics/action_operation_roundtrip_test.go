// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"bytes"
	"encoding/json"
	"testing"

	bkn "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/stretchr/testify/require"
)

func TestActionOperationBKNRoundTrip(t *testing.T) {
	at := &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
		ATID: "link_supplier", ATName: "Link supplier", ActionType: "add", ObjectTypeID: "orders",
		OperationConfig: &interfaces.ActionOperationConfig{Type: "relation", RelationTypeID: "supplier", RelationOperation: "create", AnchorRole: "source", OtherObjectCondition: &interfaces.ActionCondCfg{Field: "status", Operation: "==", ValueFrom: "input", Value: "status"}},
		Parameters:      []interfaces.Parameter{{Name: "order_id", Type: "string", ValueFrom: "source_identity"}, {Name: "supplier_id", Type: "string", ValueFrom: "target_identity"}, {Name: "status", Type: "string", ValueFrom: "input"}},
	}}
	text := bkn.SerializeActionType(ToBKNActionType(at))
	require.Contains(t, text, "### Operation Config")
	parsed, err := bkn.ParseActionTypeFile(text, "actions/link_supplier.bkn")
	require.NoError(t, err)
	result := ToADPActionType("network", "main", parsed)
	require.Equal(t, at.OperationConfig, result.OperationConfig)
	require.NoError(t, interfaces.ValidateActionOperation(result))
	require.Equal(t, at.Parameters[0].ValueFrom, result.Parameters[0].ValueFrom)
	_, err = bkn.ParseActionTypeFile("---\ntype: action_type\nid: bad\nname: Bad\n---\n## ActionType: Bad\n### Operation Config\n```yaml\noperation_config:\n  type: relation\n  unexpected_mapping: id\n```\n", "bad.bkn")
	require.Error(t, err, "unknown operation keys must not be silently discarded")
}

func TestActionTypedConstantsSurviveBKNRoundTrip(t *testing.T) {
	action := &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
		ATID: "notify", ATName: "Notify", ActionType: "add", OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
		Parameters: []interfaces.Parameter{
			{Name: "enabled", Type: "boolean", ValueFrom: "const", Value: false},
			{Name: "count", Type: "integer", ValueFrom: "const", Value: json.Number("9223372036854775807")},
			{Name: "options", Type: "object", ValueFrom: "const", Value: map[string]any{"text": "a|b", "enabled": true}},
			{Name: "text", Type: "string", ValueFrom: "const", Value: "false"},
			{Name: "null_text", Type: "string", ValueFrom: "const", Value: "null"},
		},
	}}
	parsed, err := bkn.ParseActionTypeFile(bkn.SerializeActionType(ToBKNActionType(action)), "notify.bkn")
	require.NoError(t, err)
	restored := ToADPActionType("kn", "main", parsed)
	for index, original := range action.Parameters {
		expected, err := json.Marshal(original.Value)
		require.NoError(t, err)
		actual, err := json.Marshal(restored.Parameters[index].Value)
		require.NoError(t, err)
		require.JSONEq(t, string(expected), string(actual))
	}
}

// A recognition action travels in a 2.1 package with its event class, targets and time zone; what
// the platform keeps of it does not.
func TestRecognitionActionBKNRoundTrip(t *testing.T) {
	at := &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
		ATID: "late_order_check", ATName: "识别订单超期", ActionType: "add", ActionIntent: "add", ObjectTypeID: "order",
		OperationConfig: &interfaces.ActionOperationConfig{
			Type: interfaces.ActionOperationEvent, EventClass: "late_order",
			Targets: &interfaces.ActionEventTargets{Cypher: &interfaces.ActionCypherTargets{
				Query: "MATCH (o:order) WHERE o.due < $since\nRETURN o",
				Parameters: map[string]interfaces.ActionTargetParameter{
					"since": {ValueFrom: "check_time", Value: "-PT48H"},
					"state": {ValueFrom: "const", Value: "待发货"},
				},
			}},
		},
		Schedule: interfaces.Schedule{Type: interfaces.ActionScheduleCron, Expression: "0 8 * * *", Timezone: "Asia/Shanghai"},
	},
		ExecutionSubject: &interfaces.AccountInfo{ID: "u1", Type: "user"},
		RuleVersion:      "a1b2c3d4e5f6",
		Trial:            &interfaces.ActionEventTrial{Subject: "order"},
	}
	network := &bkn.BknNetwork{
		BknNetworkFrontmatter: bkn.BknNetworkFrontmatter{Type: "knowledge_network", ID: "kn", Name: "kn"},
		ActionTypes:           []*bkn.BknActionType{ToBKNActionType(at)},
	}
	network.BknSpec = bkn.BknSpecV21
	var buf bytes.Buffer
	require.NoError(t, bkn.WriteNetworkToTar(network, &buf))
	require.NotContains(t, buf.String(), "a1b2c3d4e5f6", "the rule version is the platform's")
	require.NotContains(t, buf.String(), "execution_subject")
	loaded, err := bkn.LoadNetworkFromTar(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	require.Len(t, loaded.ActionTypes, 1)
	restored := ToADPActionType("kn", "main", loaded.ActionTypes[0])
	require.Equal(t, at.OperationConfig, restored.OperationConfig)
	require.Equal(t, at.Schedule, restored.Schedule)
	require.Nil(t, restored.ExecutionSubject)
	require.Empty(t, restored.RuleVersion)
	require.Nil(t, restored.Trial)
	require.NoError(t, interfaces.ValidateActionOperation(restored))
}
