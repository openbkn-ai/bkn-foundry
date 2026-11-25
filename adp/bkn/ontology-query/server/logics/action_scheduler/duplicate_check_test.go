// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
)

func TestDuplicateFingerprintPreservesHistoricalDigest(t *testing.T) {
	instances := []interfaces.ObjectSystemInfo{{
		InstanceIdentity: map[string]any{"z": json.Number("9007199254740993"), "a": "<id>\u2028"},
	}}
	params := map[string]any{
		"z":    []string(nil),
		"n":    json.Number("9007199254740993"),
		"html": "<>&\u2028\u2029",
	}
	got, err := computeDuplicateFingerprint(instances, params, map[string]any{"a": nil, "scope": "a&b"})
	if err != nil {
		t.Fatal(err)
	}
	const want = "46e998cb3d091a160f2aea9f7ae2bf7c9f05f655e83a8980072b0da5350c49a1"
	if got != want {
		t.Fatalf("computeDuplicateFingerprint() = %q, want %q", got, want)
	}
}

func Test_computeDuplicateFingerprint(t *testing.T) {
	Convey("computeDuplicateFingerprint is stable across order and key order", t, func() {
		a := []interfaces.ObjectSystemInfo{
			{InstanceIdentity: map[string]any{"b": "2", "a": "1"}},
			{InstanceIdentity: map[string]any{"id": "x"}},
		}
		b := []interfaces.ObjectSystemInfo{
			{InstanceIdentity: map[string]any{"id": "x"}},
			{InstanceIdentity: map[string]any{"a": "1", "b": "2"}},
		}
		ha, err := computeDuplicateFingerprint(a, nil)
		So(err, ShouldBeNil)
		hb, err := computeDuplicateFingerprint(b, nil)
		So(err, ShouldBeNil)
		So(ha, ShouldEqual, hb)
		So(ha, ShouldNotBeEmpty)

		Convey("different instances produce different hashes", func() {
			c := []interfaces.ObjectSystemInfo{
				{InstanceIdentity: map[string]any{"id": "y"}},
			}
			hc, err := computeDuplicateFingerprint(c, nil)
			So(err, ShouldBeNil)
			So(hc, ShouldNotEqual, ha)
		})

		Convey("an empty dynamic parameter map has the same fingerprint as no parameters", func() {
			empty, err := computeDuplicateFingerprint(a, map[string]any{})
			So(err, ShouldBeNil)
			So(empty, ShouldEqual, ha)

			explicit, err := computeDuplicateFingerprint(a, map[string]any{"message": ""})
			So(err, ShouldBeNil)
			So(explicit, ShouldNotEqual, ha)
		})

		Convey("same virtual instance with different dynamic_params produce different hashes", func() {
			virtual := []interfaces.ObjectSystemInfo{
				{InstanceIdentity: map[string]any{}},
			}
			h1, err := computeDuplicateFingerprint(virtual, map[string]any{"message": "hello"})
			So(err, ShouldBeNil)
			h2, err := computeDuplicateFingerprint(virtual, map[string]any{"message": "world"})
			So(err, ShouldBeNil)
			So(h1, ShouldNotEqual, h2)
		})

		Convey("dynamic_params key order does not change fingerprint", func() {
			inst := []interfaces.ObjectSystemInfo{
				{InstanceIdentity: map[string]any{"id": "1"}},
			}
			h1, err := computeDuplicateFingerprint(inst, map[string]any{"a": 1, "b": 2})
			So(err, ShouldBeNil)
			h2, err := computeDuplicateFingerprint(inst, map[string]any{"b": 2, "a": 1})
			So(err, ShouldBeNil)
			So(h1, ShouldEqual, h2)
		})
	})
}

func Test_defaultDuplicateCheck(t *testing.T) {
	Convey("defaultDuplicateCheck", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		logsService := omock.NewMockActionLogsService(mockCtrl)
		service := &actionSchedulerService{logsService: logsService}
		ctx := context.Background()
		hash := "abc123"
		req := &interfaces.ActionExecutionRequest{
			KNID:                 "kn_001",
			ActionTypeID:         "at_001",
			InstanceIdentityHash: hash,
			Instances: []interfaces.ObjectSystemInfo{
				{InstanceIdentity: map[string]any{"id": "1"}},
			},
		}

		prevWindow := duplicateWindowSeconds
		Reset(func() { duplicateWindowSeconds = prevWindow })

		Convey("rejects when pending/running execution exists in window", func() {
			duplicateWindowSeconds = 60
			logsService.EXPECT().QueryExecutions(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, q *interfaces.ActionLogQuery) (*interfaces.ActionExecutionList, error) {
					So(q.KNID, ShouldEqual, "kn_001")
					So(q.ActionTypeID, ShouldEqual, "at_001")
					So(q.InstanceIdentityHash, ShouldEqual, hash)
					So(q.Statuses, ShouldResemble, []string{
						interfaces.ExecutionStatusPending,
						interfaces.ExecutionStatusRunning,
					})
					So(len(q.StartTimeRange), ShouldEqual, 2)
					So(q.Limit, ShouldEqual, 1)
					return &interfaces.ActionExecutionList{
						Entries: []interfaces.ActionExecution{{ID: "exec_dup", Status: interfaces.ExecutionStatusPending}},
					}, nil
				},
			)

			proceed, err := service.defaultDuplicateCheck(ctx, req)
			So(err, ShouldBeNil)
			So(proceed, ShouldBeFalse)
		})

		Convey("allows when no in-flight execution in window (window expired / completed)", func() {
			duplicateWindowSeconds = 60
			logsService.EXPECT().QueryExecutions(gomock.Any(), gomock.Any()).Return(
				&interfaces.ActionExecutionList{Entries: []interfaces.ActionExecution{}}, nil,
			)

			proceed, err := service.defaultDuplicateCheck(ctx, req)
			So(err, ShouldBeNil)
			So(proceed, ShouldBeTrue)
		})

		Convey("skips check when window disabled", func() {
			duplicateWindowSeconds = 0
			proceed, err := service.defaultDuplicateCheck(ctx, req)
			So(err, ShouldBeNil)
			So(proceed, ShouldBeTrue)
		})
	})
}

func Test_ExecuteAction_DuplicateCheck(t *testing.T) {
	Convey("ExecuteAction rejects an empty parameter map when an execution with no parameters is in flight", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		omAccess := omock.NewMockOntologyManagerAccess(mockCtrl)
		ots := omock.NewMockObjectTypeService(mockCtrl)
		logsService := omock.NewMockActionLogsService(mockCtrl)
		logics.OMA = omAccess

		prevWindow := duplicateWindowSeconds
		Reset(func() { duplicateWindowSeconds = prevWindow })
		duplicateWindowSeconds = 60

		actionType := interfaces.ActionType{
			ATID:         "at_001",
			ATName:       "restart",
			ObjectTypeID: "ot_001",
			ActionType:   "modify",
			ActionSource: interfaces.ActionSource{Type: interfaces.ActionSourceTypeTool, BoxID: "box", ToolID: "tool"},
			Parameters:   []interfaces.Parameter{}, OperationConfig: &interfaces.ActionOperationConfig{Type: "object"},
		}
		omAccess.EXPECT().GetActionType(gomock.Any(), "kn_001", interfaces.MAIN_BRANCH, "at_001").
			Return(actionType, map[string]any{}, true, nil)
		omAccess.EXPECT().GetObjectType(gomock.Any(), "kn_001", interfaces.MAIN_BRANCH, "ot_001").
			Return(actionTargetObject("ot_001", "id"), true, nil).AnyTimes()

		ots.EXPECT().GetObjectsByObjectTypeID(gomock.Any(), gomock.Any()).Return(interfaces.Objects{
			Datas: []map[string]any{
				{
					interfaces.SYSTEM_PROPERTY_INSTANCE_ID:       "inst-1",
					interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY: map[string]any{"id": "pod-1"},
					interfaces.SYSTEM_PROPERTY_DISPLAY:           "pod-1",
				},
			},
			TotalCount: new(int64(1)),
		}, nil)

		request := &interfaces.ActionExecutionRequest{
			KNID:               "kn_001",
			ActionTypeID:       "at_001",
			InstanceIdentities: []map[string]any{{"id": "pod-1"}},
			DynamicParams:      map[string]any{},
		}
		targets := []interfaces.ObjectSystemInfo{{InstanceIdentity: request.InstanceIdentities[0]}}
		inFlightHash, err := computeDuplicateFingerprint(targets, nil, actionType.OperationConfig, request.ObjectInput)
		So(err, ShouldBeNil)
		logsService.EXPECT().QueryExecutions(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, query *interfaces.ActionLogQuery) (*interfaces.ActionExecutionList, error) {
				So(query.InstanceIdentityHash, ShouldEqual, inFlightHash)
				return &interfaces.ActionExecutionList{
					Entries: []interfaces.ActionExecution{{ID: "exec_old", Status: interfaces.ExecutionStatusRunning}},
				}, nil
			})

		service := &actionSchedulerService{
			omAccess:    omAccess,
			logsService: logsService,
			ots:         ots,
			proxy:       &actionProxyResolverStub{},
			permissions: &actionPermissionStub{},
		}
		service.duplicateCheckHook = service.defaultDuplicateCheck

		_, err = service.ExecuteAction(context.Background(), request)
		So(err, ShouldNotBeNil)
		httpErr, ok := err.(*httperrors.HTTPError)
		So(ok, ShouldBeTrue)
		So(httpErr.HTTPCode, ShouldEqual, http.StatusConflict)
		So(httpErr.ErrorCode, ShouldEqual, oerrors.OntologyQuery_ActionExecution_DuplicateExecution)
	})
}
