// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_ProjectStatus(t *testing.T) {
	Convey("Test projectStatus\n", t, func() {
		owner := caller
		editor := interfaces.AccountInfo{ID: "u2", Type: "user"}
		project := func() *interfaces.Project {
			return &interfaces.Project{ID: "p1", Owner: owner, CatalogIDs: []string{"c1"}}
		}
		// Running since 100; stale before 50 or before 150.
		const fresh, stale = int64(50), int64(150)
		ready := func() *interfaces.Draft {
			d := confirmableDraft()
			d.UpdateTime = 100
			return d
		}
		confirmed := func() *interfaces.Draft {
			d := ready()
			d.Items.Confirmation = &interfaces.DraftConfirmation{UpdateTime: 100, Commit: "c1", Time: 100}
			return d
		}
		checkOf := func(commit string, status string) *interfaces.Check {
			return &interfaces.Check{ProjectID: "p1", Commit: commit, Status: status, StartTime: 100}
		}
		status := func(p *interfaces.Project, d *interfaces.Draft, c *interfaces.Check, as interfaces.AccountInfo,
			staleBefore int64) *interfaces.ProjectStatus {
			return projectStatus(p, d, c, as, staleBefore, staleBefore, 7)
		}
		action := func(s *interfaces.ProjectStatus, name string) *interfaces.ProjectStatusNextAction {
			for i := range s.NextActions {
				if s.NextActions[i].Action == name {
					return &s.NextActions[i]
				}
			}
			return nil
		}
		codes := func(s *interfaces.ProjectStatus) []string {
			out := []string{}
			for _, b := range s.Blockers {
				out = append(out, b.Code)
			}
			return out
		}

		Convey("An archived project waits for its owner to unarchive it\n", func() {
			p := project()
			p.Archived = true

			s := status(p, ready(), nil, owner, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_ARCHIVED)
			So(s.NextActions, ShouldResemble, []interfaces.ProjectStatusNextAction{{
				Action: interfaces.ACTION_UNARCHIVE, Method: "POST", Path: "/projects/p1:unarchive", Allowed: true,
			}})
		})

		Convey("A project without catalogs waits for its owner to choose some\n", func() {
			p := project()
			p.CatalogIDs = nil

			s := status(p, nil, nil, editor, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_NO_DRAFT)
			So(s.Blockers, ShouldResemble, []interfaces.ProjectStatusBlocker{
				{Code: interfaces.BLOCKER_NO_CATALOGS, WaitingFor: interfaces.WAITING_FOR_OWNER},
			})
			set := action(s, interfaces.ACTION_SET_CATALOGS)
			So(set.Allowed, ShouldBeFalse)
			So(set.Reason, ShouldEqual, interfaces.ACTION_REASON_NOT_OWNER)
			So(action(s, interfaces.ACTION_GENERATE_DRAFT).Reason, ShouldEqual, interfaces.BLOCKER_NO_CATALOGS)
		})

		Convey("Catalogs emptied after drafting block only drafting again\n", func() {
			p := project()
			p.CatalogIDs = []string{}

			for _, s := range []*interfaces.ProjectStatus{
				status(p, ready(), nil, owner, fresh),
				status(p, confirmed(), checkOf("c1", interfaces.CHECK_STATUS_PASSED), owner, fresh),
			} {
				So(s.Blockers, ShouldBeEmpty)
				So(action(s, interfaces.ACTION_SET_CATALOGS), ShouldBeNil)
				generate := action(s, interfaces.ACTION_GENERATE_DRAFT)
				So(generate.Allowed, ShouldBeFalse)
				So(generate.Reason, ShouldEqual, interfaces.BLOCKER_NO_CATALOGS)
			}
			So(status(p, confirmed(), checkOf("c1", interfaces.CHECK_STATUS_PASSED), owner, fresh).Stage,
				ShouldEqual, interfaces.PROJECT_STAGE_CONFIRMED)

			failed := &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_FAILED, UpdateTime: 100}
			s := status(p, failed, nil, owner, fresh)
			So(s.Blockers, ShouldResemble, []interfaces.ProjectStatusBlocker{
				{Code: interfaces.BLOCKER_NO_CATALOGS, WaitingFor: interfaces.WAITING_FOR_OWNER},
			})
			So(action(s, interfaces.ACTION_SET_CATALOGS).Allowed, ShouldBeTrue)
		})

		Convey("A project not drafted yet can be drafted\n", func() {
			s := status(project(), nil, nil, editor, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_NO_DRAFT)
			generate := action(s, interfaces.ACTION_GENERATE_DRAFT)
			So(generate.Path, ShouldEqual, "/projects/p1/ontology/draft:generate")
			So(generate.Allowed, ShouldBeTrue)
		})

		Convey("Drafting is waited for, unless it has gone stale\n", func() {
			drafting := &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_DRAFTING, UpdateTime: 100}

			s := status(project(), drafting, nil, owner, fresh)
			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_DRAFTING)
			So(action(s, interfaces.ACTION_WAIT).RetryAfterS, ShouldBeGreaterThan, 0)

			s = status(project(), drafting, nil, owner, stale)
			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_DRAFT_FAILED)
			So(action(s, interfaces.ACTION_GENERATE_DRAFT).Allowed, ShouldBeTrue)
		})

		Convey("A failed draft can be drafted again\n", func() {
			failed := &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_FAILED, UpdateTime: 100}

			s := status(project(), failed, nil, owner, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_DRAFT_FAILED)
		})

		Convey("Pending items keep the draft in deciding, waiting for editors\n", func() {
			d := ready()
			d.Items.ObjectTypes[0].NeedsIdentity = true
			d.Items.RelationTypes = append(d.Items.RelationTypes, compileFixture().RelationTypes[2])

			s := status(project(), d, nil, owner, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_DECIDING)
			So(s.Blockers, ShouldResemble, []interfaces.ProjectStatusBlocker{
				{Code: interfaces.BLOCKER_NEEDS_IDENTITY, Count: 1, WaitingFor: interfaces.WAITING_FOR_EDITOR},
				{Code: interfaces.BLOCKER_UNADOPTED_ENDS, Count: 1, WaitingFor: interfaces.WAITING_FOR_EDITOR},
			})
			confirm := action(s, interfaces.ACTION_CONFIRM_DRAFT)
			So(confirm.Allowed, ShouldBeFalse)
			So(confirm.Reason, ShouldEqual, interfaces.BLOCKER_NEEDS_IDENTITY)
		})

		Convey("A draft adopting nothing is still being decided\n", func() {
			d := ready()
			for _, ot := range d.Items.ObjectTypes {
				ot.Adopted = false
			}
			for _, rt := range d.Items.RelationTypes {
				rt.Adopted = false
			}

			s := status(project(), d, nil, owner, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_DECIDING)
			So(codes(s), ShouldResemble, []string{interfaces.BLOCKER_NOTHING_ADOPTED})
			So(action(s, interfaces.ACTION_ADOPT_ITEMS), ShouldNotBeNil)
		})

		Convey("A ready draft is the owner's to confirm, with the body to send\n", func() {
			s := status(project(), ready(), nil, owner, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_READY_TO_CONFIRM)
			So(s.Blockers, ShouldBeEmpty)
			So(*action(s, interfaces.ACTION_CONFIRM_DRAFT), ShouldResemble, interfaces.ProjectStatusNextAction{
				Action: interfaces.ACTION_CONFIRM_DRAFT, Method: "POST", Path: "/projects/p1/ontology/draft:confirm",
				Body: map[string]any{"update_time": int64(100)}, Allowed: true,
			})
			So(action(status(project(), ready(), nil, editor, fresh), interfaces.ACTION_CONFIRM_DRAFT).Reason,
				ShouldEqual, interfaces.ACTION_REASON_NOT_OWNER)
		})

		Convey("Drafting again says how many decisions it would discard\n", func() {
			d := ready()
			d.Items.Decisions = []*interfaces.DraftDecision{{Kind: "object_types"}, {Kind: "relation_types"}}

			generate := action(status(project(), d, nil, owner, fresh), interfaces.ACTION_GENERATE_DRAFT)

			So(generate.DiscardsDecisions, ShouldEqual, 2)
			So(generate.Body, ShouldResemble, map[string]any{"discard_decisions": true})
		})

		Convey("An edit after confirming makes the confirmation out of date\n", func() {
			d := confirmed()
			d.UpdateTime = 120

			s := status(project(), d, checkOf("c1", interfaces.CHECK_STATUS_PASSED), owner, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_READY_TO_CONFIRM)
			So(s.Stale, ShouldResemble, []string{interfaces.STALE_CONFIRMATION_OUTDATED})
			So(s.Confirmation.Current, ShouldBeFalse)
		})

		Convey("A confirmation not checked yet is the owner's to check\n", func() {
			s := status(project(), confirmed(), nil, owner, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_CHECK_NOT_STARTED)
			So(action(s, interfaces.ACTION_RUN_CHECK).Path, ShouldEqual, "/projects/p1/ontology/check:run")
		})

		Convey("A check of an earlier confirmation is out of date\n", func() {
			s := status(project(), confirmed(), checkOf("c0", interfaces.CHECK_STATUS_PASSED), owner, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_CHECK_NOT_STARTED)
			So(s.Stale, ShouldResemble, []string{interfaces.STALE_CHECK_OUTDATED})
			So(s.Check.Current, ShouldBeFalse)
		})

		Convey("A running check is waited for, unless it has gone stale\n", func() {
			running := checkOf("c1", interfaces.CHECK_STATUS_RUNNING)

			s := status(project(), confirmed(), running, owner, fresh)
			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_CHECKING)
			run := action(s, interfaces.ACTION_RUN_CHECK)
			So(run.Allowed, ShouldBeFalse)
			So(run.Reason, ShouldEqual, interfaces.ACTION_REASON_RUNNING)

			s = status(project(), confirmed(), running, owner, stale)
			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_CHECK_NEEDS_ATTENTION)
			So(codes(s), ShouldResemble, []string{interfaces.BLOCKER_CHECK_FAILED})
			So(action(s, interfaces.ACTION_RUN_CHECK).Allowed, ShouldBeTrue)
		})

		Convey("A check with issues, failed or partial needs attention\n", func() {
			for status_, code := range map[string]string{
				interfaces.CHECK_STATUS_ISSUES:  interfaces.BLOCKER_CHECK_ISSUES,
				interfaces.CHECK_STATUS_FAILED:  interfaces.BLOCKER_CHECK_FAILED,
				interfaces.CHECK_STATUS_PARTIAL: interfaces.BLOCKER_CHECK_PARTIAL,
			} {
				s := status(project(), confirmed(), checkOf("c1", status_), owner, fresh)

				So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_CHECK_NEEDS_ATTENTION)
				So(codes(s), ShouldResemble, []string{code})
				So(s.Blockers[0].WaitingFor, ShouldEqual, interfaces.WAITING_FOR_OWNER)
			}
		})

		Convey("A passed check of the current confirmation is confirmed\n", func() {
			s := status(project(), confirmed(), checkOf("c1", interfaces.CHECK_STATUS_PASSED), owner, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_CONFIRMED)
			So(s.Blockers, ShouldBeEmpty)
			So(s.Stale, ShouldBeEmpty)
			So(s.Check.Current, ShouldBeTrue)
		})

		Convey("The same data gives the same status, empty lists included\n", func() {
			a, err := json.Marshal(status(project(), confirmed(), nil, owner, fresh))
			So(err, ShouldBeNil)
			b, err := json.Marshal(status(project(), confirmed(), nil, owner, fresh))
			So(err, ShouldBeNil)

			So(string(a), ShouldEqual, string(b))
			So(string(a), ShouldContainSubstring, `"stale":[]`)
			So(string(a), ShouldContainSubstring, `"links":{"check":"/projects/p1/ontology/check",`+
				`"draft":"/projects/p1/ontology/draft","pending":"/projects/p1/pending"}`)
		})
	})
}
