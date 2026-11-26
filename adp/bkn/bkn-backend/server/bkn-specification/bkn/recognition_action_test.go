// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const recognitionFile = "action_types/late_pod_check.bkn"

// recognition is the recognition action of late_pod: it selects pods by their condition every
// five minutes. It is written the same way under 2.1 and 3.0.
var recognition = "---\ntype: action_type\nid: late_pod_check\nname: 识别重启过多\ntags: []\naction_intent: add\n---\n\n" +
	"## ActionType: 识别重启过多\n\n找出最近 48 小时内创建、名称为空的 Pod。\n\n" +
	"### Operation Config\n\n```yaml\noperation_config:\n  type: event\n  event_class: late_pod\n```\n\n" +
	"### Bound Object\n\n| Bound Object |\n|--------------|\n| pod |\n\n" +
	"### Trigger Condition\n\n```yaml\nobject_type_id: pod\nfield: pod_name\noperation: ==\nvalue_from: check_time\nvalue: -PT48H\n```\n\n" +
	"### Schedule\n\n| Type | Expression | Timezone |\n|------|------------|----------|\n| FIX_RATE | 5m | Asia/Shanghai |\n"

// recognitionSpecs load a package with the event class and its recognition action, the action
// changed by replacing old with new, under each specification that has them.
func loadRecognition(t *testing.T, spec, old, new string, extra ...v3Edit) (*BknNetwork, error) {
	t.Helper()
	edits := append([]v3Edit{{file: recognitionFile, new: changed(t, recognition, old, new)}}, extra...)
	return eventSpecs[spec](t, "", "", edits...)
}

func actionTypeOf(net *BknNetwork, id string) *BknActionType {
	for _, at := range net.ActionTypes {
		if at.ID == id {
			return at
		}
	}
	return nil
}

func recognitionCodes(t *testing.T, net *BknNetwork) []string {
	t.Helper()
	codes := []string{}
	for _, issue := range ValidateNetwork(net).Errors {
		if issue.Table == "action_type:late_pod_check" || issue.Table == "action_type:restart_pod" ||
			strings.HasPrefix(issue.Table, "action_type:") {
			codes = append(codes, issue.Code)
		}
	}
	return codes
}

func TestRecognitionActionIsReadUnder21And30(t *testing.T) {
	for spec := range eventSpecs {
		t.Run(spec, func(t *testing.T) {
			net, err := loadRecognition(t, spec, "", "")
			require.NoError(t, err)
			at := actionTypeOf(net, "late_pod_check")
			require.NotNil(t, at)
			assert.True(t, at.IsRecognitionAction())
			assert.Equal(t, "late_pod", at.OperationConfig.EventClass)
			assert.Equal(t, &Schedule{Type: ScheduleTypeFixRate, Expression: "5m", Timezone: "Asia/Shanghai"}, at.Schedule)
			assert.Equal(t, ValueFromCheckTime, at.TriggerCondition.ValueFrom)
			assert.Empty(t, recognitionCodes(t, net))
		})
	}
}

func TestRecognitionActionTargetsRoundTrip(t *testing.T) {
	targets := map[string]string{
		"cypher": "  targets:\n    cypher:\n      query: |-\n        MATCH (p:pod) WHERE p.created_at < $since\n        RETURN p\n" +
			"      parameters:\n        since:\n          value_from: check_time\n          value: -PT48H\n        status:\n          value_from: const\n          value: 待发货\n",
		"function": "  targets:\n    function:\n      box_id: box_k8s\n      tool_id: tool_late\n" +
			"      parameters:\n        day:\n          value_from: check_date\n          value: -P1D\n",
		"metric": "  targets:\n    metric:\n      metric_id: restarts\n      version: 2\n      operator: '>'\n      value: 0.9\n      subject_key: hq\n",
	}
	condition := "### Trigger Condition\n\n```yaml\nobject_type_id: pod\nfield: pod_name\noperation: ==\nvalue_from: check_time\nvalue: -PT48H\n```\n\n"
	for spec := range eventSpecs {
		for kind, yaml := range targets {
			t.Run(spec+"/"+kind, func(t *testing.T) {
				net, err := loadRecognition(t, spec, "  event_class: late_pod\n```\n\n", "  event_class: late_pod\n"+yaml+"```\n\n")
				require.NoError(t, err)
				at := actionTypeOf(net, "late_pod_check")
				// A recognition action with targets has no condition.
				assert.Contains(t, recognitionCodes(t, net), "invalid_recognition_basis")
				at.TriggerCondition = nil
				assert.Empty(t, recognitionCodes(t, net))
				require.NotNil(t, at.OperationConfig.Targets)

				files := exportFiles(t, net)
				written := files[recognitionFile]
				assert.Contains(t, written, "type: event")
				assert.Contains(t, written, "| FIX_RATE | 5m | Asia/Shanghai |")
				assert.NotContains(t, written, condition)
				again, err := loadFiles(files)
				require.NoError(t, err, "the exporter's own output reads back:\n%s", written)
				assert.Equal(t, at.OperationConfig, actionTypeOf(again, "late_pod_check").OperationConfig)
				assert.Equal(t, files, exportFiles(t, again))
			})
		}
	}
}

// A 2.0 package has no recognition actions: one is refused, as the event classes it records are
// not read.
func TestRecognitionActionIsRefusedUnder20(t *testing.T) {
	_, err := ParseActionTypeFile(recognition, recognitionFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bkn_spec 2.1")

	other := strings.Replace(recognition, "  type: event\n  event_class: late_pod\n", "  type: object\n  event_class: late_pod\n", 1)
	net, err := eventSpecs[BknSpecV21](t, "", "", v3Edit{file: recognitionFile, new: other})
	require.Error(t, err, "event_class belongs to type event")
	assert.Nil(t, net)
}

// The Timezone column comes with 2.1: a 2.0 package reads the schedule as it always did, and one
// without a time zone is written as before.
func TestScheduleTimezone(t *testing.T) {
	plain := strings.Replace(recognition, "  type: event\n  event_class: late_pod\n", "  type: tool\n", 1)
	at, err := ParseActionTypeFile(plain, recognitionFile)
	require.NoError(t, err)
	assert.Equal(t, &Schedule{Type: ScheduleTypeFixRate, Expression: "5m"}, at.Schedule)

	at.Schedule.Timezone = ""
	assert.Contains(t, SerializeActionType(at), "### Schedule\n\n| Type | Expression |\n|------|------------|\n| FIX_RATE | 5m |\n")
	at.Schedule.Timezone = "UTC"
	assert.Contains(t, SerializeActionType(at), "| Type | Expression | Timezone |\n|------|------------|----------|\n| FIX_RATE | 5m | UTC |\n")
}

func TestRecognitionActionInNetwork(t *testing.T) {
	report := v3Edit{file: lateOrderFile, new: strings.Replace(lateOrder, "source: rule", "source: report", 1)}
	second := v3Edit{file: "action_types/late_pod_check_2.bkn",
		new: strings.Replace(recognition, "id: late_pod_check\n", "id: late_pod_check_2\n", 1)}
	for name, test := range map[string]struct {
		old, new string
		extra    []v3Edit
		code     string
	}{
		"no event class":           {"  event_class: late_pod\n", "", nil, "invalid_recognition_action"},
		"an unknown class":         {"event_class: late_pod", "event_class: missing", nil, "invalid_event_class_ref"},
		"a class that is no event": {"event_class: late_pod", "event_class: pod", nil, "invalid_event_class_ref"},
		"a reported class":         {"", "", []v3Edit{report}, "event_class_not_rule"},
		"another subject": {"| Bound Object |\n|--------------|\n| pod |", "| Bound Object |\n|--------------|\n| late_pod |",
			nil, "event_subject_mismatch"},
		"no condition and no targets": {"### Trigger Condition\n\n```yaml\nobject_type_id: pod\nfield: pod_name\noperation: ==\nvalue_from: check_time\nvalue: -PT48H\n```\n\n",
			"", nil, "invalid_recognition_basis"},
		"two kinds of targets": {"  event_class: late_pod\n", "  event_class: late_pod\n  targets:\n    cypher:\n      query: MATCH (p:pod) RETURN p\n    metric:\n      metric_id: m\n      version: 1\n      operator: '<'\n      value: 1\n",
			nil, "invalid_recognition_basis"},
		"an input in the condition": {"value_from: check_time\nvalue: -PT48H", "value_from: input\nvalue: x", nil, "invalid_recognition_basis"},
		"a wrong offset":            {"value: -PT48H", "value: 48 hours", nil, "invalid_recognition_basis"},
		"no schedule":               {"| FIX_RATE | 5m | Asia/Shanghai |\n", "", nil, "invalid_recognition_schedule"},
		"no time zone":              {"| FIX_RATE | 5m | Asia/Shanghai |", "| FIX_RATE | 5m | |", nil, "invalid_recognition_schedule"},
		"an unknown time zone":      {"Asia/Shanghai", "Mars/Olympus", nil, "invalid_recognition_schedule"},
		"too often":                 {"| FIX_RATE | 5m |", "| FIX_RATE | 1m |", nil, "invalid_recognition_schedule"},
		"a six-field cron":          {"| FIX_RATE | 5m |", "| CRON | 0 0 8 * * * |", nil, "invalid_recognition_schedule"},
		"an action source": {"### Schedule", "### Action Source\n\n| Type | BoxID | ToolID | McpID | ToolName |\n|------|-------|--------|-------|----------|\n| tool | b | t | | |\n\n### Schedule",
			nil, "recognition_action_source"},
		"a parameter": {"### Schedule", "### Parameter Binding\n\n| Name | Type | Source | Operation | ValueFrom | Value | Description |\n|------|------|--------|-----------|-----------|-------|-------------|\n| x | string | Body | | input | | |\n\n### Schedule",
			nil, "recognition_action_source"},
		"a modify intent":        {"action_intent: add", "action_intent: modify", nil, "invalid_recognition_action"},
		"two of the same class":  {"", "", []v3Edit{second}, "duplicate_recognition_action"},
		"a reserved cypher name": {"  event_class: late_pod\n", "  event_class: late_pod\n  targets:\n    cypher:\n      query: MATCH (p:pod) RETURN p\n      parameters:\n        check_time:\n          value_from: const\n          value: x\n", nil, "invalid_recognition_basis"},
	} {
		for spec := range eventSpecs {
			t.Run(spec+"/"+name, func(t *testing.T) {
				net, err := loadRecognition(t, spec, test.old, test.new, test.extra...)
				require.NoError(t, err)
				assert.Contains(t, recognitionCodes(t, net), test.code)
			})
		}
	}
}

// Only a recognition action has a check time to compare with.
func TestCheckTimeBelongsToRecognitionActions(t *testing.T) {
	net, err := eventSpecs[BknSpecV21](t, "", "", v3Edit{file: "action_types/restart_pod.bkn",
		new: "---\ntype: action_type\nid: restart_pod\nname: 重启Pod\ntags: []\naction_intent: modify\n---\n\n## ActionType: 重启Pod\n\n" +
			"### Operation Config\n\n```yaml\noperation_config:\n  type: object\n```\n\n" +
			"### Bound Object\n\n| Bound Object |\n|--------------|\n| pod |\n\n" +
			"### Trigger Condition\n\n```yaml\nfield: pod_name\noperation: ==\nvalue_from: check_date\n```\n"})
	require.NoError(t, err)
	assert.Contains(t, recognitionCodes(t, net), "invalid_action_condition")
}

// A 2.0 export leaves the recognition actions out, as a 2.0 package cannot hold them; 2.1 keeps
// them.
func TestRecognitionActionIsLeftOutOfA20Export(t *testing.T) {
	net, err := loadRecognition(t, BknSpecV21, "", "")
	require.NoError(t, err)
	assert.Contains(t, exportFiles(t, net), recognitionFile)

	net.BknSpec = BknSpecLegacy
	files := exportFiles(t, net)
	assert.NotContains(t, files, recognitionFile)
	assert.NotContains(t, files["network.bkn"], "late_pod_check")
	require.NotNil(t, actionTypeOf(net, "late_pod_check"), "the network itself keeps the action")
	_, err = loadFiles(files)
	require.NoError(t, err, "the 2.0 package reads back")
}
