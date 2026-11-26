// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
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
	got, err := computeDuplicateFingerprints(instances, params, map[string]any{"a": nil, "scope": "a&b"})
	if err != nil {
		t.Fatal(err)
	}
	const want = "46e998cb3d091a160f2aea9f7ae2bf7c9f05f655e83a8980072b0da5350c49a1"
	if got.primary != want || got.alternate != "" {
		t.Fatalf("computeDuplicateFingerprints() = %+v, want only %q", got, want)
	}
}

func TestDuplicateFingerprintNormalizesRelationCondition(t *testing.T) {
	instances := []interfaces.ObjectSystemInfo{{InstanceIdentity: map[string]any{"id": "one"}}}
	objectInput := map[string]any{"id": "one"}
	operation := &interfaces.ActionOperationConfig{
		Type: "relation", RelationTypeID: "membership", RelationOperation: "create", AnchorRole: "source",
		OtherObjectCondition: &cond.CondCfg{Name: "status", Operation: "==", Value: "open"},
	}
	// This is one of the byte sequences emitted before condition keys were sorted.
	const legacyInput = `{"id":"one"}
dynamic_params:null
action_context:[{"type":"relation","relation_type_id":"membership","relation_operation":"create","anchor_role":"source","other_object_condition":{"field":"status","operation":"==","value":"open"}},{"id":"one"}]`
	legacyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(legacyInput)))
	for range 64 {
		hash, err := computeDuplicateFingerprints(instances, nil, operation, objectInput)
		if err != nil {
			t.Fatal(err)
		}
		if hash.primary != legacyHash {
			t.Fatalf("fingerprint = %s, want %s", hash.primary, legacyHash)
		}
	}

	operation.OtherObjectCondition.Value = "closed"
	changed, err := computeDuplicateFingerprints(instances, nil, operation, objectInput)
	if err != nil {
		t.Fatal(err)
	}
	if changed.primary == legacyHash {
		t.Fatal("changing the relation condition must change the fingerprint")
	}
}

func Test_computeDuplicateFingerprints(t *testing.T) {
	Convey("computeDuplicateFingerprints is stable across order and key order", t, func() {
		a := []interfaces.ObjectSystemInfo{
			{InstanceIdentity: map[string]any{"b": "2", "a": "1"}},
			{InstanceIdentity: map[string]any{"id": "x"}},
		}
		b := []interfaces.ObjectSystemInfo{
			{InstanceIdentity: map[string]any{"id": "x"}},
			{InstanceIdentity: map[string]any{"a": "1", "b": "2"}},
		}
		ha, err := computeDuplicateFingerprints(a, nil)
		So(err, ShouldBeNil)
		hb, err := computeDuplicateFingerprints(b, nil)
		So(err, ShouldBeNil)
		So(ha, ShouldEqual, hb)
		So(ha.primary, ShouldNotBeEmpty)

		Convey("different instances produce different hashes", func() {
			c := []interfaces.ObjectSystemInfo{
				{InstanceIdentity: map[string]any{"id": "y"}},
			}
			hc, err := computeDuplicateFingerprints(c, nil)
			So(err, ShouldBeNil)
			So(hc, ShouldNotEqual, ha)
		})

		Convey("omitted and empty dynamic parameters use the same primary and historical fingerprints", func() {
			empty, err := computeDuplicateFingerprints(a, map[string]any{})
			So(err, ShouldBeNil)
			So(empty, ShouldEqual, ha)
			So(empty.alternate, ShouldNotBeEmpty)
			So(empty.alternate, ShouldNotEqual, empty.primary)

			explicit, err := computeDuplicateFingerprints(a, map[string]any{"message": ""})
			So(err, ShouldBeNil)
			So(explicit.primary, ShouldNotEqual, ha.primary)
			So(explicit.primary, ShouldNotEqual, ha.alternate)
			So(explicit.alternate, ShouldBeEmpty)
		})

		Convey("same virtual instance with different dynamic_params produce different hashes", func() {
			virtual := []interfaces.ObjectSystemInfo{
				{InstanceIdentity: map[string]any{}},
			}
			h1, err := computeDuplicateFingerprints(virtual, map[string]any{"message": "hello"})
			So(err, ShouldBeNil)
			h2, err := computeDuplicateFingerprints(virtual, map[string]any{"message": "world"})
			So(err, ShouldBeNil)
			So(h1, ShouldNotEqual, h2)
		})

		Convey("dynamic_params key order does not change fingerprint", func() {
			inst := []interfaces.ObjectSystemInfo{
				{InstanceIdentity: map[string]any{"id": "1"}},
			}
			h1, err := computeDuplicateFingerprints(inst, map[string]any{"a": 1, "b": 2})
			So(err, ShouldBeNil)
			h2, err := computeDuplicateFingerprints(inst, map[string]any{"b": 2, "a": 1})
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
	const historicalInput = `{"id":"pod-1"}
dynamic_params:%s
action_context:[{"type":"object"},null]`
	nullHash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf(historicalInput, "null"))))
	emptyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf(historicalInput, "{}"))))

	for _, tc := range []struct {
		name           string
		params         map[string]any
		inFlightHash   string
		inFlightStatus string
	}{
		{name: "empty request matches historical empty", params: map[string]any{}, inFlightHash: emptyHash, inFlightStatus: interfaces.ExecutionStatusRunning},
		{name: "omitted parameters match historical empty", inFlightHash: emptyHash, inFlightStatus: interfaces.ExecutionStatusPending},
		{name: "empty request matches normalized null", params: map[string]any{}, inFlightHash: nullHash, inFlightStatus: interfaces.ExecutionStatusRunning},
		{name: "omitted parameters match normalized null", inFlightHash: nullHash, inFlightStatus: interfaces.ExecutionStatusPending},
		{name: "empty request writes the fingerprint read by the previous release", params: map[string]any{}},
		{name: "omitted parameters write the fingerprint read by the previous release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Convey("ExecuteAction checks both historical fingerprints and writes the normalized one", t, func() {
				mockCtrl := gomock.NewController(t)
				omAccess := omock.NewMockOntologyManagerAccess(mockCtrl)
				ots := omock.NewMockObjectTypeService(mockCtrl)
				logsService := omock.NewMockActionLogsService(mockCtrl)
				prevOMA := logics.OMA
				prevWindow := duplicateWindowSeconds
				Reset(func() {
					logics.OMA = prevOMA
					duplicateWindowSeconds = prevWindow
				})
				logics.OMA = omAccess
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
					Datas: []map[string]any{{
						interfaces.SYSTEM_PROPERTY_INSTANCE_ID:       "inst-1",
						interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY: map[string]any{"id": "pod-1"},
						interfaces.SYSTEM_PROPERTY_DISPLAY:           "pod-1",
					}},
					TotalCount: new(int64(1)),
				}, nil)
				logsService.EXPECT().QueryExecutions(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, query *interfaces.ActionLogQuery) (*interfaces.ActionExecutionList, error) {
						if tc.inFlightHash != "" {
							So(query.InstanceIdentityHash, ShouldEqual, nullHash)
							So(query.AlternateInstanceIdentityHash, ShouldEqual, emptyHash)
						}
						if tc.inFlightHash == query.InstanceIdentityHash || tc.inFlightHash == query.AlternateInstanceIdentityHash {
							return &interfaces.ActionExecutionList{
								Entries: []interfaces.ActionExecution{{
									ID: "exec_old", Status: tc.inFlightStatus, InstanceIdentityHash: tc.inFlightHash,
								}},
							}, nil
						}
						return &interfaces.ActionExecutionList{}, nil
					})

				wantCode := http.StatusConflict
				wantError := oerrors.OntologyQuery_ActionExecution_DuplicateExecution
				if tc.inFlightHash == "" {
					// Stop after examining the pending record so the asynchronous worker is not started.
					logsService.EXPECT().CreateExecution(gomock.Any(), gomock.Any()).DoAndReturn(
						func(_ context.Context, execution *interfaces.ActionExecution) error {
							So(execution.InstanceIdentityHash, ShouldEqual, nullHash)
							return errors.New("stop at pending execution")
						})
					wantCode = http.StatusInternalServerError
					wantError = oerrors.OntologyQuery_ActionExecution_CreateExecutionFailed
				}

				service := &actionSchedulerService{
					omAccess:    omAccess,
					logsService: logsService,
					ots:         ots,
					proxy:       &actionProxyResolverStub{},
					permissions: &actionPermissionStub{},
				}
				service.duplicateCheckHook = service.defaultDuplicateCheck
				request := &interfaces.ActionExecutionRequest{
					KNID:               "kn_001",
					ActionTypeID:       "at_001",
					InstanceIdentities: []map[string]any{{"id": "pod-1"}},
					DynamicParams:      tc.params,
				}

				_, err := service.ExecuteAction(context.Background(), request)
				httpErr, ok := errors.AsType[*httperrors.HTTPError](err)
				So(ok, ShouldBeTrue)
				So(httpErr.HTTPCode, ShouldEqual, wantCode)
				So(httpErr.ErrorCode, ShouldEqual, wantError)
			})
		})
	}
}
