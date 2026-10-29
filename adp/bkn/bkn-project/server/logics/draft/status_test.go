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

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
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
			return projectStatus(p, d, c, nil, as, staleBefore, staleBefore, 7)
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

		Convey("Placeholder ids are counted, and do not block confirming\n", func() {
			So(status(project(), ready(), nil, owner, fresh).Draft.Placeholders, ShouldEqual, 0)

			d := ready()
			d.Items.Inputs = &interfaces.DraftInputs{IDs: &interfaces.DraftInputIDs{Count: 5, Placeholders: 3, Fingerprint: "f"}}
			s := status(project(), d, nil, owner, fresh)
			So(s.Draft.Placeholders, ShouldEqual, 3)
			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_READY_TO_CONFIRM)
			So(s.Blockers, ShouldBeEmpty)
		})

		Convey("Confirming is refused ahead for what the branch would refuse\n", func() {
			p := project()
			p.KNID, p.Branch = "supply", "project/p1"
			existing := &interfaces.DefinitionsPreflight{Existing: []string{"object_types/orders.bkn"}}

			s := projectStatus(p, ready(), nil, existing, owner, fresh, fresh, 7)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_READY_TO_CONFIRM)
			So(s.Blockers, ShouldResemble, []interfaces.ProjectStatusBlocker{
				{Code: interfaces.BLOCKER_DEFINITIONS_EXIST, Count: 1, WaitingFor: interfaces.WAITING_FOR_EDITOR},
			})
			confirm := action(s, interfaces.ACTION_CONFIRM_DRAFT)
			So(confirm.Allowed, ShouldBeFalse)
			So(confirm.Reason, ShouldEqual, interfaces.BLOCKER_DEFINITIONS_EXIST)

			s = projectStatus(p, ready(), nil, &interfaces.DefinitionsPreflight{NotV3: interfaces.DEFINITIONS_BRANCH_NOT_V3},
				owner, fresh, fresh, 7)
			So(s.Blockers[0].Code, ShouldEqual, interfaces.BLOCKER_BRANCH_NOT_V3)
			So(*action(s, interfaces.ACTION_SYNC_BRANCH), ShouldResemble, interfaces.ProjectStatusNextAction{
				Action: interfaces.ACTION_SYNC_BRANCH, Method: "POST", Path: "/projects/p1/branch:sync", Allowed: true,
			})
			s = projectStatus(p, ready(), nil, &interfaces.DefinitionsPreflight{NotV3: interfaces.DEFINITIONS_BRANCH_NOT_V3},
				editor, fresh, fresh, 7)
			So(action(s, interfaces.ACTION_SYNC_BRANCH).Allowed, ShouldBeFalse)
			So(action(s, interfaces.ACTION_SYNC_BRANCH).Reason, ShouldEqual, interfaces.ACTION_REASON_NOT_OWNER)

			s = projectStatus(p, ready(), nil, &interfaces.DefinitionsPreflight{NotV3: interfaces.DEFINITIONS_LINEAGE_NOT_V3},
				owner, fresh, fresh, 7)
			So(s.Blockers[0].Code, ShouldEqual, interfaces.BLOCKER_LINEAGE_NOT_V3)
			So(*action(s, interfaces.ACTION_COMMIT_DRIFT), ShouldResemble, interfaces.ProjectStatusNextAction{
				Action: interfaces.ACTION_COMMIT_DRIFT, Method: "POST", Path: "/projects/p1/drift:commit", Allowed: true,
			})
			s = projectStatus(p, ready(), nil, &interfaces.DefinitionsPreflight{NotV3: interfaces.DEFINITIONS_LINEAGE_NOT_V3},
				editor, fresh, fresh, 7)
			So(action(s, interfaces.ACTION_COMMIT_DRIFT).Reason, ShouldEqual, interfaces.ACTION_REASON_NOT_OWNER)
		})

		Convey("Confirming a partial draft says it accepts what drafting could not read\n", func() {
			d := ready()
			d.Items.Inputs = &interfaces.DraftInputs{Partial: true}

			s := status(project(), d, nil, owner, fresh)

			So(s.Blockers, ShouldBeEmpty)
			confirm := action(s, interfaces.ACTION_CONFIRM_DRAFT)
			So(confirm.Allowed, ShouldBeTrue)
			So(confirm.Body, ShouldResemble, map[string]any{"update_time": int64(100), "accept_partial": true})
			So(confirm.Accepts, ShouldResemble, []string{interfaces.ACCEPTS_PARTIAL_DRAFT})
		})

		Convey("Confirming with undecided items says how many and that it accepts leaving them out\n", func() {
			d := ready()
			d.Items.Decisions = nil

			s := status(project(), d, nil, owner, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_READY_TO_CONFIRM)
			So(s.Undecided, ShouldEqual, 1)
			So(s.Blockers, ShouldBeEmpty)
			confirm := action(s, interfaces.ACTION_CONFIRM_DRAFT)
			So(confirm.Allowed, ShouldBeTrue)
			So(confirm.Body["accept_undecided"], ShouldEqual, true)
			So(confirm.Accepts, ShouldResemble, []string{interfaces.ACCEPTS_UNDECIDED_ITEMS})
		})

		Convey("A decision on the draft as a whole is not discarded by drafting again\n", func() {
			d := ready()
			d.Items.Decisions = []*interfaces.DraftDecision{
				{Kind: interfaces.DRAFT_KIND_DRAFT, Action: interfaces.DRAFT_DECISION_ACCEPT_PARTIAL},
			}

			generate := action(status(project(), d, nil, owner, fresh), interfaces.ACTION_GENERATE_DRAFT)

			So(generate.DiscardsDecisions, ShouldEqual, 0)
			So(generate.Body, ShouldBeNil)
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
				// Only a partial check can be accepted when publishing, so only then is a release reviewed.
				So(action(s, interfaces.ACTION_REVIEW_RELEASE) != nil, ShouldEqual, status_ == interfaces.CHECK_STATUS_PARTIAL)
			}
		})

		Convey("A passed check of the current confirmation is confirmed\n", func() {
			s := status(project(), confirmed(), checkOf("c1", interfaces.CHECK_STATUS_PASSED), owner, fresh)

			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_CONFIRMED)
			So(s.Blockers, ShouldBeEmpty)
			So(s.Stale, ShouldBeEmpty)
			So(s.Check.Current, ShouldBeTrue)
			So(*action(s, interfaces.ACTION_REVIEW_RELEASE), ShouldResemble, interfaces.ProjectStatusNextAction{
				Action: interfaces.ACTION_REVIEW_RELEASE, Method: "GET", Path: "/projects/p1/release-review", Allowed: true,
			})
		})

		Convey("A release by the project of its current confirmation makes it published\n", func() {
			passed := checkOf("c1", interfaces.CHECK_STATUS_PASSED)
			release := &interfaces.Release{ProjectID: "p1", Version: "1.1.0", NetworkID: "supply_v1_1_0", CreateTime: 100}

			Convey("Consumers not yet on it wait for the owner to switch them\n", func() {
				s := status(project(), confirmed(), passed, owner, fresh)
				withRelease(s, project(), release, nil, owner)

				So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_PUBLISHED)
				So(s.Release, ShouldResemble, &interfaces.ProjectStatusRelease{Version: "1.1.0", NetworkID: "supply_v1_1_0"})
				So(action(s, interfaces.ACTION_REVIEW_RELEASE), ShouldBeNil)
				So(*action(s, interfaces.ACTION_SWITCH_CONSUMERS), ShouldResemble, interfaces.ProjectStatusNextAction{
					Action: interfaces.ACTION_SWITCH_CONSUMERS, Method: "POST", Path: "/projects/p1/switches",
					Body: map[string]any{"to_network_id": "supply_v1_1_0"}, Allowed: true,
				})

				s = status(project(), confirmed(), passed, editor, fresh)
				withRelease(s, project(), release, &interfaces.Switch{ToNetworkID: "supply"}, editor)
				So(action(s, interfaces.ACTION_SWITCH_CONSUMERS).Reason, ShouldEqual, interfaces.ACTION_REASON_NOT_OWNER)
			})

			Convey("Consumers already on it leave nothing to switch\n", func() {
				s := status(project(), confirmed(), passed, owner, fresh)
				withRelease(s, project(), release, &interfaces.Switch{ToNetworkID: "supply_v1_1_0"}, owner)

				So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_PUBLISHED)
				So(s.Release.InUse, ShouldBeTrue)
				So(action(s, interfaces.ACTION_SWITCH_CONSUMERS), ShouldBeNil)
			})

			Convey("A partial check published is published, its blocker settled\n", func() {
				s := status(project(), confirmed(), checkOf("c1", interfaces.CHECK_STATUS_PARTIAL), owner, fresh)
				withRelease(s, project(), release, nil, owner)

				So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_PUBLISHED)
				So(s.Blockers, ShouldBeEmpty)
			})

			Convey("Until published, reviewing names the next version after the lineage's newest release\n", func() {
				for _, c := range []struct {
					latest *interfaces.Release
					want   string
				}{
					{nil, "1.0.0"},
					{&interfaces.Release{ProjectID: "p2", Version: "1.0.4", CreateTime: 100}, "1.0.5"},
					{&interfaces.Release{ProjectID: "p1", Version: "2.3.9", CreateTime: 99}, "2.3.10"},
				} {
					s := status(project(), confirmed(), passed, owner, fresh)
					withRelease(s, project(), c.latest, nil, owner)

					So(*action(s, interfaces.ACTION_REVIEW_RELEASE), ShouldResemble, interfaces.ProjectStatusNextAction{
						Action: interfaces.ACTION_REVIEW_RELEASE, Method: "GET",
						Path: "/projects/p1/release-review?version=" + c.want, Allowed: true,
						Requires: []string{interfaces.ACTION_REQUIRES_VERSION}, SuggestedVersion: c.want,
					})
				}
			})

			Convey("Another project's release, an earlier one, or an outdated confirmation is not this one\n", func() {
				other := *release
				other.ProjectID = "p2"
				earlier := *release
				earlier.CreateTime = 99
				outdated := confirmed()
				outdated.UpdateTime = 120
				for _, c := range []struct {
					draft   *interfaces.Draft
					release *interfaces.Release
				}{
					{confirmed(), &other}, {confirmed(), &earlier}, {outdated, release}, {confirmed(), nil},
				} {
					s := status(project(), c.draft, passed, owner, fresh)
					stage := s.Stage
					withRelease(s, project(), c.release, nil, owner)

					So(s.Stage, ShouldEqual, stage)
					So(s.Release, ShouldBeNil)
				}
			})
		})

		Convey("The same data gives the same status, empty lists included\n", func() {
			a, err := json.Marshal(status(project(), confirmed(), nil, owner, fresh))
			So(err, ShouldBeNil)
			b, err := json.Marshal(status(project(), confirmed(), nil, owner, fresh))
			So(err, ShouldBeNil)

			So(string(a), ShouldEqual, string(b))
			So(string(a), ShouldContainSubstring, `"stale":[]`)
			So(string(a), ShouldContainSubstring, `"links":{"check":"/projects/p1/ontology/check",`+
				`"draft":"/projects/p1/ontology/draft","index":"/projects/p1/ontology/index","pending":"/projects/p1/pending"}`)
		})
	})
}

func Test_GetStatus(t *testing.T) {
	Convey("Test draftService GetStatus\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ca := pmock.NewMockCheckAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		ls := pmock.NewMockLineageService(ctrl)
		ra := pmock.NewMockReleaseAccess(ctrl)
		// The project's requirement specification; none unless a test sets one.
		rqa := pmock.NewMockRequirementAccess(ctrl)
		var requirement *interfaces.Requirement
		var requirementErr error
		rqa.EXPECT().GetRequirement(gomock.Any(), "p1").DoAndReturn(
			func(context.Context, string) (*interfaces.Requirement, bool, error) {
				return requirement, requirement != nil, requirementErr
			}).AnyTimes()
		svc := &draftService{setting: testSetting, da: da, ca: ca, ps: ps, ls: ls, ra: ra, rqa: rqa}
		ctx := context.Background()
		action := func(s *interfaces.ProjectStatus, name string) *interfaces.ProjectStatusNextAction {
			for i := range s.NextActions {
				if s.NextActions[i].Action == name {
					return &s.NextActions[i]
				}
			}
			return nil
		}
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{
			ID: "p1", KNID: "supply", Branch: "project/p1", Owner: caller, CatalogIDs: []string{"c1"},
		}}

		Convey("A draft ready to confirm is preflighted with the paths confirming would write\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			d := confirmableDraft()
			d.Items.Confirmation = &interfaces.DraftConfirmation{Files: []string{"object_types/old.bkn"},
				Pending: []string{"object_types/stopped.bkn"}}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, true, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil)
			ls.EXPECT().PreflightDefinitions(gomock.Any(), "supply", "project/p1", compiledPaths(),
				[]string{"object_types/old.bkn", "object_types/stopped.bkn"}).
				Return(&interfaces.DefinitionsPreflight{Existing: []string{"object_types/orders.bkn"}}, nil)

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_READY_TO_CONFIRM)
			So(s.Blockers[0].Code, ShouldEqual, interfaces.BLOCKER_DEFINITIONS_EXIST)
		})

		Convey("Unresolved requirement conflicts are counted, and confirming accepts them\n", func() {
			requirement = &interfaces.Requirement{Version: 2, Status: interfaces.REQUIREMENT_STATUS_READY,
				Spec: interfaces.RequirementSpec{Conflicts: []*interfaces.RequirementConflict{
					{ID: "k1"},
					{ID: "k2", Deferred: true, Resolution: &interfaces.RequirementResolution{Reason: "ask"}},
					{ID: "k3", Resolution: &interfaces.RequirementResolution{DocumentID: "pol", Reason: "policy"}},
				}}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil)
			ls.EXPECT().PreflightDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&interfaces.DefinitionsPreflight{}, nil)

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(s.UnresolvedConflicts, ShouldEqual, 2)
			confirm := action(s, interfaces.ACTION_CONFIRM_DRAFT)
			So(confirm.Body["accept_unresolved_conflicts"], ShouldEqual, true)
			So(confirm.Accepts, ShouldContain, interfaces.ACCEPTS_UNRESOLVED_CONFLICTS)
			// Conflicts are a risk to accept, not a blocker.
			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_READY_TO_CONFIRM)
		})

		Convey("Without a ready specification nothing is accepted for conflicts\n", func() {
			for _, spec := range []*interfaces.Requirement{nil, {Status: interfaces.REQUIREMENT_STATUS_DRAFTING,
				Spec: interfaces.RequirementSpec{Conflicts: []*interfaces.RequirementConflict{{ID: "k1"}}}}} {
				requirement = spec
				ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
				ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil)
				ls.EXPECT().PreflightDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&interfaces.DefinitionsPreflight{}, nil)

				s, err := svc.GetStatus(ctx, token, "p1", caller)

				So(err, ShouldBeNil)
				So(s.UnresolvedConflicts, ShouldEqual, 0)
				So(action(s, interfaces.ACTION_CONFIRM_DRAFT).Accepts, ShouldNotContain, interfaces.ACCEPTS_UNRESOLVED_CONFLICTS)
			}
		})

		Convey("With no draft to confirm, conflicts are not counted\n", func() {
			requirement = &interfaces.Requirement{Status: interfaces.REQUIREMENT_STATUS_READY,
				Spec: interfaces.RequirementSpec{Conflicts: []*interfaces.RequirementConflict{{ID: "k1"}}}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil)

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(s.UnresolvedConflicts, ShouldEqual, 0)
		})

		Convey("A specification that cannot be read fails the status\n", func() {
			requirementErr = errors.New("db down")
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil)
			ls.EXPECT().PreflightDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&interfaces.DefinitionsPreflight{}, nil).AnyTimes()

			_, err := svc.GetStatus(ctx, token, "p1", caller)

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError)
		})

		Convey("Other stages read nothing of the branch\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil)

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_NO_DRAFT)
		})

		Convey("A preflight that fails leaves the status without what it would tell\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil)
			ls.EXPECT().PreflightDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(nil, errors.New("git unavailable"))

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_READY_TO_CONFIRM)
			So(s.Blockers, ShouldBeEmpty)
		})

		Convey("A current confirmation is read against the lineage's newest release and switch\n", func() {
			d := confirmableDraft()
			d.Items.Confirmation = &interfaces.DraftConfirmation{UpdateTime: d.UpdateTime, Commit: "c1", Time: 100}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, true, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(&interfaces.Check{Commit: "c1", Status: interfaces.CHECK_STATUS_PASSED}, true, nil)

			Convey("A release of it is published\n", func() {
				ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "supply", Limit: 1}).Return(
					[]*interfaces.Release{{ProjectID: "p1", Version: "1.1.0", NetworkID: "supply_v1_1_0", CreateTime: 200}}, nil)
				ra.EXPECT().GetLatestSwitch(gomock.Any(), "supply").Return(&interfaces.Switch{ToNetworkID: "supply_v1_1_0"}, true, nil)

				s, err := svc.GetStatus(ctx, token, "p1", caller)

				So(err, ShouldBeNil)
				So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_PUBLISHED)
				So(s.Release.InUse, ShouldBeTrue)
			})

			Convey("Before any release, reviewing suggests the first version\n", func() {
				ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
				ra.EXPECT().GetLatestSwitch(gomock.Any(), "supply").Return(nil, false, nil)

				s, err := svc.GetStatus(ctx, token, "p1", caller)

				So(err, ShouldBeNil)
				So(s.Stage, ShouldEqual, interfaces.PROJECT_STAGE_CONFIRMED)
				for _, a := range s.NextActions {
					if a.Action == interfaces.ACTION_REVIEW_RELEASE {
						So(a.Path, ShouldEqual, "/projects/p1/release-review?version=1.0.0")
					}
				}
			})

			Convey("A failure to read the releases is an internal error\n", func() {
				ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))

				_, err := svc.GetStatus(ctx, token, "p1", caller)

				shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError)
			})

			Convey("A failure to read the switches is an internal error\n", func() {
				ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
				ra.EXPECT().GetLatestSwitch(gomock.Any(), "supply").Return(nil, false, errors.New("db down"))

				_, err := svc.GetStatus(ctx, token, "p1", caller)

				shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError)
			})
		})

		Convey("A project the caller cannot see answers as the project service does\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").
				Return(nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Project_NotFound))

			_, err := svc.GetStatus(ctx, token, "p1", caller)

			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Project_NotFound)
		})
	})
}
