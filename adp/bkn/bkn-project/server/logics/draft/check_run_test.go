// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// flakyCounter fails each table.field listed in failOnce the first time it is counted.
type flakyCounter struct {
	*fakeCounter
	failOnce map[string]bool
	counted  []string
}

func (f *flakyCounter) keyStats(ctx context.Context, table *interfaces.VegaTable, field string) (int64, int64, int64, error) {
	f.counted = append(f.counted, table.ID+"."+field)
	if f.failOnce[table.ID+"."+field] {
		delete(f.failOnce, table.ID+"."+field)
		return 0, 0, 0, errors.New("lost connection")
	}
	return f.fakeCounter.keyStats(ctx, table, field)
}

func Test_StartCheck(t *testing.T) {
	Convey("Test draftService startCheck\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ca := pmock.NewMockCheckAccess(ctrl)
		var ran []func()
		svc := &draftService{setting: common.DraftSetting{CheckTimeout: time.Minute}, ca: ca,
			run: func(fn func()) { ran = append(ran, fn) }}
		ctx := context.Background()
		input := checkInputOf(checkFixture())

		Convey("A confirmation starts a check of its commit, run as the confirmer\n", func() {
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil)
			var started interfaces.Check
			var staleBefore int64
			ca.EXPECT().StartCheck(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, c *interfaces.Check, stale int64) (bool, error) {
					started, staleBefore = *c, stale
					return true, nil
				})
			before := time.Now().UnixMilli()

			svc.startCheck(ctx, "p1", "c1", input, caller)

			So(started.Commit, ShouldEqual, "c1")
			So(started.Status, ShouldEqual, interfaces.CHECK_STATUS_RUNNING)
			So(started.RunID, ShouldNotBeEmpty)
			So(started.Attempt, ShouldEqual, 1)
			So(started.Runner, ShouldResemble, caller)
			var stored checkInput
			So(json.Unmarshal(started.Input, &stored), ShouldBeNil)
			So(stored, ShouldResemble, *input)
			// Two attempts, each with its own time, and a minute to record them.
			So(staleBefore, ShouldBeGreaterThanOrEqualTo, before-(3*time.Minute).Milliseconds())
			So(staleBefore, ShouldBeLessThanOrEqualTo, time.Now().UnixMilli()-(3*time.Minute).Milliseconds())
			So(len(ran), ShouldEqual, 1)
		})

		Convey("The run outlives the request that confirmed\n", func() {
			va := pmock.NewMockVegaAccess(ctrl)
			svc.va = va
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil)
			ca.EXPECT().StartCheck(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				DoAndReturn(func(c context.Context, _ interfaces.VegaAccount, _ string, _ int) ([]map[string]any, error) {
					So(c.Err(), ShouldBeNil)
					return []map[string]any{{"total": 10, "distinct_values": 10, "nulls": 0, "orphans": 0}}, nil
				}).AnyTimes()
			var finished interfaces.Check
			ca.EXPECT().FinishCheck(gomock.Any(), gomock.Any()).
				DoAndReturn(func(c context.Context, check *interfaces.Check) (bool, error) {
					So(c.Err(), ShouldBeNil)
					finished = *check
					return true, nil
				})
			request, cancel := context.WithCancel(ctx)

			svc.startCheck(request, "p1", "c1", input, caller)
			cancel()
			ran[0]()

			So(finished.Status, ShouldEqual, interfaces.CHECK_STATUS_PASSED)
		})

		Convey("A run replaced by a later one stops counting, and is not retried\n", func() {
			va := pmock.NewMockVegaAccess(ctrl)
			svc.va = va
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil).Times(2)
			ca.EXPECT().StartCheck(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
			// The replaced run queries nothing more: every item it had left fails as cut short.
			va.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			var replaced interfaces.Check
			ca.EXPECT().FinishCheck(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, check *interfaces.Check) (bool, error) {
					replaced = *check
					return false, nil
				})

			svc.startCheck(ctx, "p1", "c1", input, caller)
			svc.startCheck(ctx, "p1", "c2", input, caller)
			ran[0]()

			So(replaced.Commit, ShouldEqual, "c1")
			So(replaced.Attempt, ShouldEqual, 1)
			// The later run is still the project's.
			current, ok := svc.checkRuns.Load("p1")
			So(ok, ShouldBeTrue)
			So(current.(*checkRun).runID, ShouldNotEqual, replaced.RunID)
		})

		Convey("A commit already checked is not checked again\n", func() {
			for _, status := range []string{interfaces.CHECK_STATUS_PASSED, interfaces.CHECK_STATUS_ISSUES,
				interfaces.CHECK_STATUS_PARTIAL} {
				ca.EXPECT().GetCheck(gomock.Any(), "p1").
					Return(&interfaces.Check{ProjectID: "p1", Commit: "c1", Status: status}, true, nil)

				svc.startCheck(ctx, "p1", "c1", input, caller)
			}

			So(ran, ShouldBeEmpty)
		})

		Convey("A commit whose check failed, or another commit, is checked\n", func() {
			ca.EXPECT().GetCheck(gomock.Any(), "p1").
				Return(&interfaces.Check{ProjectID: "p1", Commit: "c1", Status: interfaces.CHECK_STATUS_FAILED}, true, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").
				Return(&interfaces.Check{ProjectID: "p1", Commit: "c0", Status: interfaces.CHECK_STATUS_PASSED}, true, nil)
			ca.EXPECT().StartCheck(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(2)

			svc.startCheck(ctx, "p1", "c1", input, caller)
			svc.startCheck(ctx, "p1", "c1", input, caller)

			So(len(ran), ShouldEqual, 2)
		})

		Convey("Nothing runs when the check is running already or cannot be written\n", func() {
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil).Times(2)
			ca.EXPECT().StartCheck(gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil)
			ca.EXPECT().StartCheck(gomock.Any(), gomock.Any(), gomock.Any()).Return(false, errors.New("db down"))

			svc.startCheck(ctx, "p1", "c1", input, caller)
			svc.startCheck(ctx, "p1", "c1", input, caller)

			So(ran, ShouldBeEmpty)
		})
	})
}

func Test_RunCheckJob(t *testing.T) {
	Convey("Test draftService check\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ca := pmock.NewMockCheckAccess(ctrl)
		svc := &draftService{setting: common.DraftSetting{CheckTimeout: time.Minute}, ca: ca}
		ctx := context.Background()
		input := checkInputOf(checkFixture())
		counter := &flakyCounter{fakeCounter: &fakeCounter{failed: map[string]bool{}}, failOnce: map[string]bool{}}
		running := func() *interfaces.Check {
			return &interfaces.Check{ProjectID: "p1", Commit: "c1", Status: interfaces.CHECK_STATUS_RUNNING, RunID: "r1"}
		}
		var finished interfaces.Check
		finish := func(written bool) {
			ca.EXPECT().FinishCheck(gomock.Any(), gomock.Any()).
				DoAndReturn(func(c context.Context, check *interfaces.Check) (bool, error) {
					So(c.Err(), ShouldBeNil)
					finished = *check
					return written, nil
				})
		}

		Convey("A check that passes is recorded after one attempt\n", func() {
			finish(true)

			svc.check(ctx, running(), input, counter)

			So(finished.RunID, ShouldEqual, "r1")
			So(finished.Status, ShouldEqual, interfaces.CHECK_STATUS_PASSED)
			So(finished.Attempt, ShouldEqual, 1)
			So(finished.FinishTime, ShouldBeGreaterThan, 0)
			var results checkResults
			So(json.Unmarshal(finished.Results, &results), ShouldBeNil)
			So(len(results.Keys), ShouldEqual, 3)
		})

		Convey("A failed attempt is retried once, counting only what failed\n", func() {
			counter.failOnce["res_customers.customer_id"] = true
			finish(true)

			svc.check(ctx, running(), input, counter)

			So(finished.Status, ShouldEqual, interfaces.CHECK_STATUS_PASSED)
			So(finished.Attempt, ShouldEqual, 2)
			So(counter.counted, ShouldResemble, []string{
				"res_orders.order_id", "res_customers.customer_id", "res_products.product_id",
				"res_customers.customer_id",
			})
		})

		Convey("A check failing twice stops, failed\n", func() {
			counter.failed["res_customers"] = true
			finish(true)

			svc.check(ctx, running(), input, counter)

			So(finished.Status, ShouldEqual, interfaces.CHECK_STATUS_FAILED)
			So(finished.Attempt, ShouldEqual, 2)
		})

		Convey("A check not counted in full is not retried: counting again would not count it\n", func() {
			partial := checkFixture()
			partial.ObjectTypes[1].Table.CatalogID = "other"
			finish(true)

			svc.check(ctx, running(), checkInputOf(partial), counter)

			So(finished.Status, ShouldEqual, interfaces.CHECK_STATUS_PARTIAL)
			So(finished.Attempt, ShouldEqual, 1)
		})

		Convey("A check with issues is not retried\n", func() {
			counter.keys = map[string][3]int64{"res_customers.customer_id": {60, 58, 0}}
			finish(true)

			svc.check(ctx, running(), input, counter)

			So(finished.Status, ShouldEqual, interfaces.CHECK_STATUS_ISSUES)
			So(finished.Attempt, ShouldEqual, 1)
		})

		Convey("A run a later one replaced records nothing, and that is no error\n", func() {
			finish(false)

			So(func() { svc.check(ctx, running(), input, counter) }, ShouldNotPanic)
		})

	})
}

func Test_CheckService(t *testing.T) {
	Convey("Test draftService GetCheck and RunCheck\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ca := pmock.NewMockCheckAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		var ran []func()
		svc := &draftService{setting: common.DraftSetting{CheckTimeout: time.Minute}, ca: ca, da: da, ps: ps,
			run: func(fn func()) { ran = append(ran, fn) }}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller}}
		confirmed := func() *interfaces.Draft {
			d := confirmableDraft()
			d.Items.Confirmation = &interfaces.DraftConfirmation{UpdateTime: readTime, Commit: "c1"}
			return d
		}
		var started interfaces.Check
		var run *interfaces.Check
		starts := func(ok bool) {
			ca.EXPECT().StartCheck(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, c *interfaces.Check, _ int64) (bool, error) {
					started, run = *c, c
					return ok, nil
				})
		}

		Convey("GetCheck returns the project's check to whoever sees the project\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(&interfaces.Check{ProjectID: "p1", Commit: "c1"}, true, nil)

			check, err := svc.GetCheck(ctx, token, "p1")

			So(err, ShouldBeNil)
			So(check.Commit, ShouldEqual, "c1")
		})

		Convey("GetCheck reports a project not checked yet\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil)

			_, err := svc.GetCheck(ctx, token, "p1")

			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Check_NotFound)
		})

		Convey("RunCheck checks the confirmed draft again, even a commit that passed\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmed(), true, nil)
			// The commit confirmed passed already: a person asking again is checked again.
			ca.EXPECT().GetCheck(gomock.Any(), "p1").
				Return(&interfaces.Check{ProjectID: "p1", Commit: "c1", Status: interfaces.CHECK_STATUS_PASSED}, true, nil)
			starts(true)

			check, err := svc.RunCheck(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			// The reply is the check as started, not the run's own, which the run goes on writing.
			So(check, ShouldNotPointTo, run)
			So(check.Status, ShouldEqual, interfaces.CHECK_STATUS_RUNNING)
			So(started.Commit, ShouldEqual, "c1")
			var input checkInput
			So(json.Unmarshal(started.Input, &input), ShouldBeNil)
			So(input, ShouldResemble, *checkInputOf(&confirmed().Items))
			So(len(ran), ShouldEqual, 1)
		})

		Convey("RunCheck reads what was confirmed from the last check of that commit once the draft changed\n", func() {
			edited := confirmed()
			edited.UpdateTime = readTime + 5
			edited.Items.ObjectTypes[0].Name = "Edited"
			snapshot := json.RawMessage(`{"object_types":[],"relation_types":[]}`)
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(edited, true, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(&interfaces.Check{
				ProjectID: "p1", Commit: "c1", Status: interfaces.CHECK_STATUS_FAILED, Input: snapshot,
			}, true, nil)
			starts(true)

			_, err := svc.RunCheck(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(string(started.Input), ShouldEqual, string(snapshot))
		})

		Convey("RunCheck asks to confirm again when what was confirmed is no longer known\n", func() {
			edited := confirmed()
			edited.UpdateTime = readTime + 5
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(edited, true, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil)

			_, err := svc.RunCheck(ctx, token, "p1", caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Check_ConfirmAgain)
		})

		Convey("RunCheck refuses a draft never confirmed\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)

			_, err := svc.RunCheck(ctx, token, "p1", caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Check_NothingConfirmed)
		})

		Convey("RunCheck refuses while the commit is being checked\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmed(), true, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil)
			starts(false)

			_, err := svc.RunCheck(ctx, token, "p1", caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Check_Running)
			So(ran, ShouldBeEmpty)
		})

		Convey("RunCheck is the owner's\n", func() {
			other := interfaces.AccountInfo{ID: "u2", Type: "user"}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)

			_, err := svc.RunCheck(ctx, token, "p1", other)

			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Check_NotOwner)
		})

		Convey("RunCheck refuses an archived project\n", func() {
			archived := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, Archived: true}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(archived, nil)

			_, err := svc.RunCheck(ctx, token, "p1", caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_ProjectArchived)
		})
	})
}
