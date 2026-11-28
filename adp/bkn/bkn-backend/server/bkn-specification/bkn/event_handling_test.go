// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const restartPodFile = "action_types/restart_pod.bkn"

// restartPod is an action type on the subject of late_pod that asks for nothing.
var restartPod = "---\ntype: action_type\nid: restart_pod\nname: 重启Pod\ntags: []\naction_intent: modify\n---\n\n## ActionType: 重启Pod\n\n" +
	"### Operation Config\n\n```yaml\noperation_config:\n  type: object\n```\n\n" +
	"### Bound Object\n\n| Bound Object |\n|--------------|\n| pod |\n"

const handlingNote = "  handling_note: 查看最近的发布。\n"

// asksForInput is restart_pod with an input, reason, that its caller gives.
var asksForInput = restartPod + "\n### Parameter Binding\n\n| Name | Type | Source | Operation | ValueFrom | Value | Description |\n" +
	"|------|------|--------|-----------|-----------|-------|-------------|\n| reason | string | Body | | input | | |\n"

// boundTo handles the events with restart_pod, giving its input reason the value written.
func boundTo(mode, reason string) string {
	return handledBy("restart_pod", mode) + "    parameters:\n      reason:\n" + reason
}

func handledBy(actionTypeID, mode string) string {
	return handlingNote + "  handling_action:\n    action_type_id: " + actionTypeID + "\n    mode: " + mode + "\n"
}

func handlingCodes(t *testing.T, net *BknNetwork) []string {
	t.Helper()
	codes := []string{}
	for _, issue := range ValidateNetwork(net).Errors {
		if issue.Table == "object_type:late_pod" {
			codes = append(codes, issue.Code)
		}
	}
	return codes
}

// A class names the action type that handles its events and how; it exports and reads back.
func TestEventHandlingActionRoundTrips(t *testing.T) {
	for spec, load := range eventSpecs {
		for _, mode := range []string{EventHandlingAuto, EventHandlingSuggest} {
			t.Run(spec+"/"+mode, func(t *testing.T) {
				net, err := load(t, handlingNote, handledBy("restart_pod", mode), v3Edit{file: restartPodFile, new: restartPod})
				require.NoError(t, err)
				class := objectTypeOf(net, "late_pod")
				assert.Equal(t, &EventHandlingAction{ActionTypeID: "restart_pod", Mode: mode}, class.Event.HandlingAction)
				assert.Empty(t, handlingCodes(t, net))

				files := exportFiles(t, net)
				assert.Contains(t, files[lateOrderFile], "  handling_action:\n    action_type_id: restart_pod\n    mode: "+mode+"\n")
				again, err := loadFiles(files)
				require.NoError(t, err)
				assert.Equal(t, class.Event, objectTypeOf(again, "late_pod").Event)
			})
		}
	}
}

func TestEventHandlingActionIsRefusedWhereItIsWrittenWrong(t *testing.T) {
	for name, edit := range map[string]string{
		"no action type":  handlingNote + "  handling_action:\n    mode: auto\n",
		"an unknown mode": handledBy("restart_pod", "later"),
		"an unknown key":  handlingNote + "  handling_action:\n    action_type_id: restart_pod\n    mode: auto\n    retries: 3\n",
	} {
		for spec, load := range eventSpecs {
			t.Run(spec+"/"+name, func(t *testing.T) {
				_, err := load(t, handlingNote, edit, v3Edit{file: restartPodFile, new: restartPod})
				require.Error(t, err)
			})
		}
	}
	// A reported event is not found by a check: nothing runs its handling action by itself.
	_, err := eventSpecs[BknSpecV21](t, "source: rule", "source: report")
	require.NoError(t, err)
	_, err = eventSpecs[BknSpecV21](t, "source: rule\n  subject: pod\n  severity: high\n  merge_hours: 1.5\n"+handlingNote,
		"source: report\n  subject: pod\n  severity: high\n  merge_hours: 1.5\n"+handledBy("restart_pod", EventHandlingAuto),
		v3Edit{file: restartPodFile, new: restartPod})
	require.ErrorContains(t, err, "only a class whose source is rule")
}

// The handling action is an action type of the network that acts on the subject, records no
// events, and, run by the platform, asks for no input.
func TestEventHandlingActionInNetwork(t *testing.T) {
	onAnotherObject := changed(t, restartPod, "| pod |", "| node |")
	for name, c := range map[string]struct {
		mode, action, file, code string
	}{
		"a missing action type":     {EventHandlingAuto, "drain_pod", restartPod, "invalid_handling_action_ref"},
		"another object type":       {EventHandlingSuggest, "restart_pod", onAnotherObject, "invalid_handling_action"},
		"input when run by itself":  {EventHandlingAuto, "restart_pod", asksForInput, "invalid_handling_action"},
		"the recognition action":    {EventHandlingAuto, "late_pod_check", restartPod, "invalid_handling_action"},
		"input offered to a person": {EventHandlingSuggest, "restart_pod", asksForInput, ""},
	} {
		t.Run(name, func(t *testing.T) {
			net, err := eventSpecs[BknSpecV21](t, handlingNote, handledBy(c.action, c.mode),
				v3Edit{file: restartPodFile, new: c.file}, v3Edit{file: recognitionFile, new: recognition})
			require.NoError(t, err)
			if c.code == "" {
				assert.Empty(t, handlingCodes(t, net))
				return
			}
			assert.Equal(t, []string{c.code}, handlingCodes(t, net))
		})
	}
}

// An input of the handling action takes a constant or a property of the object; it exports and
// reads back, and lets the action run by itself.
func TestBoundInputsRoundTrip(t *testing.T) {
	for spec, load := range eventSpecs {
		for name, c := range map[string]struct {
			yaml string
			want EventHandlingParameter
		}{
			"a property": {"        value_from: property\n        value: pod_name\n", EventHandlingParameter{ValueFrom: "property", Value: "pod_name"}},
			"a constant": {"        value_from: const\n        value: 3\n", EventHandlingParameter{ValueFrom: "const", Value: 3}},
		} {
			t.Run(spec+"/"+name, func(t *testing.T) {
				net, err := load(t, handlingNote, boundTo(EventHandlingAuto, c.yaml), v3Edit{file: restartPodFile, new: asksForInput})
				require.NoError(t, err)
				class := objectTypeOf(net, "late_pod")
				assert.Equal(t, map[string]EventHandlingParameter{"reason": c.want}, class.Event.HandlingAction.Parameters)
				assert.Empty(t, handlingCodes(t, net))

				files := exportFiles(t, net)
				assert.Contains(t, files[lateOrderFile], "    parameters:\n      reason:\n"+c.yaml)
				again, err := loadFiles(files)
				require.NoError(t, err)
				assert.Equal(t, class.Event, objectTypeOf(again, "late_pod").Event)
			})
		}
	}
}

func TestBoundInputsAreRefusedWhereTheyAreWrittenWrong(t *testing.T) {
	for name, c := range map[string]struct{ yaml, want string }{
		"an unknown source":     {"        value_from: input\n        value: x\n", "value_from of input reason"},
		"a constant of nothing": {"        value_from: const\n", "input reason needs a value"},
		"a property unnamed":    {"        value_from: property\n        value: \" \"\n", "needs the name of the property"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := eventSpecs[BknSpecV21](t, handlingNote, boundTo(EventHandlingAuto, c.yaml), v3Edit{file: restartPodFile, new: asksForInput})
			require.ErrorContains(t, err, c.want)
		})
	}
}

// A bound input must be an input of the action, and a property it takes one of the subject's.
func TestBoundInputsInNetwork(t *testing.T) {
	for name, c := range map[string]struct {
		mode, yaml, file, want string
	}{
		"not an input":           {EventHandlingSuggest, "        value_from: const\n        value: x\n", restartPod, "asks for no input"},
		"a missing property":     {EventHandlingAuto, "        value_from: property\n        value: node\n", asksForInput, "which \"pod\" does not have"},
		"suggested with a value": {EventHandlingSuggest, "        value_from: property\n        value: pod_name\n", asksForInput, ""},
	} {
		t.Run(name, func(t *testing.T) {
			net, err := eventSpecs[BknSpecV21](t, handlingNote, boundTo(c.mode, c.yaml), v3Edit{file: restartPodFile, new: c.file})
			require.NoError(t, err)
			var messages []string
			for _, issue := range ValidateNetwork(net).Errors {
				if issue.Table == "object_type:late_pod" {
					messages = append(messages, issue.Message)
				}
			}
			if c.want == "" {
				assert.Empty(t, messages)
				return
			}
			require.Len(t, messages, 1)
			assert.Contains(t, messages[0], c.want)
		})
	}
}
