// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

const (
	reviewToken = "Bearer test-token"
	networkBKN  = "---\ntype: knowledge_network\nid: supply\nname: Supply chain\n---\n\n# Supply chain\n"
)

var reviewOwner = interfaces.AccountInfo{ID: "u1", Type: "user"}

// objectTypeBKN is an object type mapped to a table; with mapped false its property maps no field.
func objectTypeBKN(id string, name string, mapped bool) []byte {
	field := "id"
	if !mapped {
		field = ""
	}
	return []byte(fmt.Sprintf(`---
type: object_type
id: %s
name: %s
---

## ObjectType: %s

### Data Source

| Type | ID | Name |
|------|----|------|
| resource | view_%s | %s_view |

### Data Properties

| Name | Display Name | Type | Description | Mapped Field |
|------|--------------|------|-------------|--------------|
| id | id | string | Identifier | %s |

### Keys

Primary Keys: id
Display Key: id
Incremental Key:
`, id, name, name, id, id, field))
}

func relationTypeBKN(id string, name string) []byte {
	return []byte(fmt.Sprintf(`---
type: relation_type
id: %s
name: %s
---

## RelationType: %s

### Endpoint

| Source | Target | Type |
|--------|--------|------|
| supplier | purchase_order | direct |

### Mapping Rules

| Source Property | Target Property |
|-----------------|-----------------|
| id | supplier_id |
`, id, name, name))
}

func item(kind string, id string, name string) interfaces.ReleaseItem {
	return interfaces.ReleaseItem{Kind: kind, ID: id, Name: name}
}

func checkCodes(review *interfaces.ReleaseReview) []string {
	codes := []string{}
	for _, c := range review.Checks {
		codes = append(codes, c.Code)
	}
	return codes
}

func checkOf(review *interfaces.ReleaseReview, code string) interfaces.ReleaseCheck {
	for _, c := range review.Checks {
		if c.Code == code {
			return c
		}
	}
	return interfaces.ReleaseCheck{}
}

func Test_ProjectReleaseService_ReviewRelease(t *testing.T) {
	Convey("Test projectReleaseService ReviewRelease\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ps := pmock.NewMockProjectService(mockCtrl)
		drs := pmock.NewMockDraftService(mockCtrl)
		ls := pmock.NewMockLineageService(mockCtrl)
		da := pmock.NewMockDraftAccess(mockCtrl)
		ca := pmock.NewMockCheckAccess(mockCtrl)
		gs := pmock.NewMockGitStore(mockCtrl)
		svc := &projectReleaseService{ps: ps, drs: drs, ls: ls, da: da, ca: ca, gs: gs}
		ctx := context.Background()

		project := &interfaces.ProjectEntry{Project: &interfaces.Project{
			ID: "p1", KNID: "supply", Branch: "project/p1", Owner: reviewOwner,
		}}
		mainFiles := map[string][]byte{
			"network.bkn":               []byte(networkBKN),
			"object_types/part.bkn":     objectTypeBKN("part", "Part", true),
			"object_types/supplier.bkn": objectTypeBKN("supplier", "Supplier", true),
		}
		branchFiles := map[string][]byte{
			"network.bkn":                                    []byte(networkBKN),
			"object_types/part.bkn":                          objectTypeBKN("part", "Part", true),
			"object_types/supplier.bkn":                      objectTypeBKN("supplier", "Supplier", true),
			"object_types/purchase_order.bkn":                objectTypeBKN("purchase_order", "Purchase Order", true),
			"relation_types/supplier_has_purchase_order.bkn": relationTypeBKN("supplier_has_purchase_order", "Supplier has purchase order"),
		}
		written := []string{"object_types/purchase_order.bkn", "relation_types/supplier_has_purchase_order.bkn"}
		preview := &interfaces.ReleasePreview{
			LineageID: "supply", Branch: "project/p1", Version: "1.1.0", Tag: "v1.1.0",
			NetworkID: "supply_v1_1_0", NetworkName: "Supply chain · 1.1.0", LiveNetworkID: "supply_v1_0_0",
			BaseVersion: "1.0.0", MainCommit: "m1", BranchCommit: "b1",
			Changes:      interfaces.PathDiff{Added: written},
			Capabilities: []interfaces.CapabilityPlan{},
			Refusals:     []interfaces.ReleaseRefusal{},
		}
		draft := &interfaces.Draft{ProjectID: "p1", UpdateTime: 100, Items: interfaces.DraftItems{
			Confirmation: &interfaces.DraftConfirmation{UpdateTime: 100, Commit: "c1", Files: written},
		}}
		check := &interfaces.Check{ProjectID: "p1", Commit: "c1", Status: interfaces.CHECK_STATUS_PASSED,
			Results: []byte(`{"relations":[{"relation_type":"supplier_has_purchase_order","joins":[{"orphans":3},{"orphans":1}]}]}`)}
		index := &interfaces.ProjectIndex{TasksVisible: true, ObjectTypes: []interfaces.ProjectIndexItem{
			{ID: "purchase_order", State: interfaces.INDEX_STATE_READY},
		}, Counts: map[string]int{interfaces.INDEX_STATE_READY: 1}}

		// expect sets up the reads a review makes; with no draft no index is read.
		expect := func() {
			ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil)
			ls.EXPECT().PreviewRelease(gomock.Any(), reviewToken, "supply", "project/p1", "1.1.0").Return(preview, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").DoAndReturn(
				func(context.Context, string) (*interfaces.Draft, bool, error) { return draft, draft != nil, nil })
			ca.EXPECT().GetCheck(gomock.Any(), "p1").DoAndReturn(
				func(context.Context, string) (*interfaces.Check, bool, error) { return check, check != nil, nil })
			gs.EXPECT().ReadTree(gomock.Any(), "supply", "b1").Return(branchFiles, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "supply", "m1").Return(mainFiles, nil)
		}
		expectIndex := func() {
			drs.EXPECT().GetIndex(gomock.Any(), reviewToken, "p1", reviewOwner).Return(index, nil)
		}
		review := func() *interfaces.ReleaseReview {
			r, err := svc.ReviewRelease(ctx, reviewToken, "p1", "1.1.0", reviewOwner)
			So(err, ShouldBeNil)
			return r
		}

		Convey("A release whose checks all pass lists the seven checks, what it adds and its risks\n", func() {
			expect()
			expectIndex()

			r := review()

			So(checkCodes(r), ShouldResemble, []string{
				interfaces.RELEASE_CHECK_CONFIRMED, interfaces.RELEASE_CHECK_MAPPED, interfaces.RELEASE_CHECK_NAMES,
				interfaces.RELEASE_CHECK_DRIFT, interfaces.RELEASE_CHECK_CHECK, interfaces.RELEASE_CHECK_INDEX,
				interfaces.RELEASE_CHECK_BASELINE,
			})
			for _, c := range r.Checks {
				So(c.Passed, ShouldBeTrue)
				So(c.Reason, ShouldBeEmpty)
			}
			So(r.Accepts, ShouldBeEmpty)
			So(r.Blocked, ShouldBeFalse)
			So(r.Refusals, ShouldBeEmpty)
			So(r.ProjectID, ShouldEqual, "p1")
			So(r.Version, ShouldEqual, "1.1.0")
			So(r.Changes.Added, ShouldResemble, []interfaces.ReleaseItem{
				item(interfaces.RELEASE_ITEM_OBJECT_TYPE, "purchase_order", "Purchase Order"),
				item(interfaces.RELEASE_ITEM_RELATION_TYPE, "supplier_has_purchase_order", "Supplier has purchase order"),
			})
			So(r.Changes.Changed, ShouldBeEmpty)
			So(r.Changes.Removed, ShouldBeEmpty)
			So(r.Risks.OnlyAdditions, ShouldBeTrue)
			So(r.Risks.Existing, ShouldBeEmpty)
			So(r.Risks.Orphans, ShouldResemble, []interfaces.ReleaseOrphans{{
				ReleaseItem: item(interfaces.RELEASE_ITEM_RELATION_TYPE, "supplier_has_purchase_order", "Supplier has purchase order"),
				Orphans:     4,
			}})
			So(r.Release, ShouldResemble, interfaces.ReleaseTarget{
				Tag: "v1.1.0", NetworkID: "supply_v1_1_0", NetworkName: "Supply chain · 1.1.0",
				LiveNetworkID: "supply_v1_0_0", BaseVersion: "1.0.0", MainCommit: "m1", BranchCommit: "b1",
			})
			So(r.Signature, ShouldNotBeEmpty)
		})

		Convey("The signature stays while nothing changes and moves with the branch, main or draft\n", func() {
			expect()
			expectIndex()
			first := review().Signature
			expect()
			expectIndex()
			So(review().Signature, ShouldEqual, first)

			for _, change := range []func(){
				func() { preview.BranchCommit = "b2" },
				func() { preview.MainCommit = "m2" },
				func() { draft.UpdateTime, draft.Items.Confirmation.UpdateTime = 101, 101 },
			} {
				saved, savedDraft := *preview, *draft
				savedConfirmation := *draft.Items.Confirmation
				change()
				ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil)
				ls.EXPECT().PreviewRelease(gomock.Any(), reviewToken, "supply", "project/p1", "1.1.0").Return(preview, nil)
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
				ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(check, true, nil)
				gs.EXPECT().ReadTree(gomock.Any(), "supply", gomock.Any()).Return(branchFiles, nil).Times(2)
				expectIndex()
				So(review().Signature, ShouldNotEqual, first)
				*preview, *draft = saved, savedDraft
				*draft.Items.Confirmation = savedConfirmation
			}
		})

		Convey("The scenario questions are read from the specification, which the signature follows\n", func() {
			rs := pmock.NewMockRequirementService(mockCtrl)
			svc.rs = rs
			sq := &interfaces.ScenarioQuestions{SpecVersion: 2, UpdateTime: 50, Questions: []interfaces.ScenarioQuestion{{
				ID: "q1", Text: "Which supplier has this purchase order?", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
				Needs: []interfaces.RequirementItemRef{
					{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: "purchase_order"},
					{Kind: interfaces.RELEASE_ITEM_RELATION_TYPE, ID: "supplier_has_purchase_order"},
				},
			}}}
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(sq, nil).Times(2)
			expect()
			expectIndex()

			r := review()

			So(r.Questions.SpecVersion, ShouldEqual, 2)
			So(r.Questions.AnswerableNew, ShouldHaveLength, 1)
			So(r.Questions.AnswerableNew[0].ID, ShouldEqual, "q1")
			first := r.Signature

			sq.UpdateTime = 51
			expect()
			expectIndex()
			So(review().Signature, ShouldNotEqual, first)
		})

		Convey("A failure to read the scenario questions is an internal error\n", func() {
			rs := pmock.NewMockRequirementService(mockCtrl)
			svc.rs = rs
			ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil)
			ls.EXPECT().PreviewRelease(gomock.Any(), reviewToken, "supply", "project/p1", "1.1.0").Return(preview, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(check, true, nil)
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(nil, errors.New("db down"))

			_, err := svc.ReviewRelease(ctx, reviewToken, "p1", "1.1.0", reviewOwner)

			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusInternalServerError)
		})

		Convey("Without a specification service the review has no questions\n", func() {
			expect()
			expectIndex()

			r := review()

			So(r.Questions.Total, ShouldEqual, 0)
			So(r.Questions.AnswerableNew, ShouldBeEmpty)
		})

		Convey("Without a confirmation nothing about it can pass, and the index is not read\n", func() {
			draft = nil
			expect()

			r := review()

			So(checkOf(r, interfaces.RELEASE_CHECK_CONFIRMED).Reason, ShouldEqual, interfaces.RELEASE_REASON_NOT_CONFIRMED)
			So(checkOf(r, interfaces.RELEASE_CHECK_MAPPED).Reason, ShouldEqual, interfaces.RELEASE_REASON_NOT_CONFIRMED)
			So(checkOf(r, interfaces.RELEASE_CHECK_CHECK).Reason, ShouldEqual, interfaces.RELEASE_REASON_CHECK_NOT_RUN)
			So(checkOf(r, interfaces.RELEASE_CHECK_INDEX).Reason, ShouldEqual, interfaces.RELEASE_REASON_NOT_CONFIRMED)
			So(checkOf(r, interfaces.RELEASE_CHECK_CONFIRMED).Accept, ShouldBeEmpty)
			So(r.Blocked, ShouldBeTrue)
		})

		Convey("A confirmation of an earlier draft is outdated and blocks\n", func() {
			draft.UpdateTime = 101
			expect()
			expectIndex()

			r := review()

			c := checkOf(r, interfaces.RELEASE_CHECK_CONFIRMED)
			So(c.Passed, ShouldBeFalse)
			So(c.Reason, ShouldEqual, interfaces.RELEASE_REASON_CONFIRMATION_OUTDATED)
			So(r.Blocked, ShouldBeTrue)
		})

		Convey("An object type the confirmation wrote that maps no table or field blocks, by name\n", func() {
			branchFiles["object_types/purchase_order.bkn"] = objectTypeBKN("purchase_order", "Purchase Order", false)
			expect()
			expectIndex()

			r := review()

			c := checkOf(r, interfaces.RELEASE_CHECK_MAPPED)
			So(c.Passed, ShouldBeFalse)
			So(c.Reason, ShouldEqual, interfaces.RELEASE_REASON_MAPPING_MISSING)
			So(c.Details, ShouldResemble, []interfaces.ReleaseItem{
				item(interfaces.RELEASE_ITEM_OBJECT_TYPE, "purchase_order", "Purchase Order"),
			})
			So(r.Blocked, ShouldBeTrue)
		})

		Convey("An object type the confirmation wrote that is bound to no table blocks too\n", func() {
			unbound := strings.Replace(string(objectTypeBKN("purchase_order", "Purchase Order", true)),
				"| resource | view_purchase_order | purchase_order_view |\n", "", 1)
			branchFiles["object_types/purchase_order.bkn"] = []byte(unbound)
			expect()
			expectIndex()

			r := review()

			So(checkOf(r, interfaces.RELEASE_CHECK_MAPPED).Reason, ShouldEqual, interfaces.RELEASE_REASON_MAPPING_MISSING)
		})

		Convey("A name the same as one on the branch blocks; a similar one can be accepted\n", func() {
			Convey("Same\n", func() {
				branchFiles["object_types/po.bkn"] = objectTypeBKN("po", "purchase order", true)
				expect()
				expectIndex()

				r := review()

				c := checkOf(r, interfaces.RELEASE_CHECK_NAMES)
				So(c.Reason, ShouldEqual, interfaces.RELEASE_REASON_DUPLICATE_NAMES)
				So(c.Accept, ShouldBeEmpty)
				So(c.Details, ShouldResemble, map[string][]interfaces.SimilarNames{
					"same": {{
						Added: item(interfaces.RELEASE_ITEM_OBJECT_TYPE, "purchase_order", "Purchase Order"),
						Other: item(interfaces.RELEASE_ITEM_OBJECT_TYPE, "po", "purchase order"),
					}},
					"similar": {},
				})
				So(r.Blocked, ShouldBeTrue)
			})
			Convey("Similar\n", func() {
				branchFiles["object_types/po.bkn"] = objectTypeBKN("po", "Purchase Orders", true)
				expect()
				expectIndex()

				r := review()

				c := checkOf(r, interfaces.RELEASE_CHECK_NAMES)
				So(c.Reason, ShouldEqual, interfaces.RELEASE_REASON_SIMILAR_NAMES)
				So(c.Accept, ShouldEqual, interfaces.RELEASE_ACCEPT_SIMILAR_NAMES)
				So(r.Accepts, ShouldResemble, []string{interfaces.RELEASE_ACCEPT_SIMILAR_NAMES})
				So(r.Blocked, ShouldBeFalse)
			})
		})

		Convey("Drift and a moved live version come from the release's own checks; other refusals block\n", func() {
			report := &interfaces.DriftReport{LineageID: "supply", KNID: "supply_v1_0_0"}
			preview.Refusals = []interfaces.ReleaseRefusal{
				{Code: perrors.BknProject_Release_VersionExists, Details: map[string]any{"tag": "v1.1.0"}},
				{Code: perrors.BknProject_Release_BranchBehind},
				{Code: perrors.BknProject_Release_Drift, Details: report},
			}
			expect()
			expectIndex()

			r := review()

			drift := checkOf(r, interfaces.RELEASE_CHECK_DRIFT)
			So(drift.Passed, ShouldBeFalse)
			So(drift.Reason, ShouldEqual, interfaces.RELEASE_REASON_DRIFT)
			So(drift.Details, ShouldEqual, report)
			baseline := checkOf(r, interfaces.RELEASE_CHECK_BASELINE)
			So(baseline.Passed, ShouldBeFalse)
			So(baseline.Reason, ShouldEqual, interfaces.RELEASE_REASON_BRANCH_BEHIND)
			So(baseline.Details, ShouldResemble, map[string]any{"base_version": "1.0.0", "live_network_id": "supply_v1_0_0"})
			So(r.Refusals, ShouldResemble, []interfaces.ReleaseRefusal{
				{Code: perrors.BknProject_Release_VersionExists, Details: map[string]any{"tag": "v1.1.0"}},
			})
			So(r.Blocked, ShouldBeTrue)
		})

		Convey("A refusal of the release blocks it though every check passes\n", func() {
			preview.Refusals = []interfaces.ReleaseRefusal{{Code: perrors.BknProject_Release_NetworkExists}}
			expect()
			expectIndex()

			r := review()

			for _, c := range r.Checks {
				So(c.Passed, ShouldBeTrue)
			}
			So(r.Refusals, ShouldResemble, preview.Refusals)
			So(r.Blocked, ShouldBeTrue)
		})

		Convey("The check of the confirmation decides; only a partial one can be accepted\n", func() {
			for status, want := range map[string][2]string{
				interfaces.CHECK_STATUS_RUNNING: {interfaces.RELEASE_REASON_CHECK_RUNNING, ""},
				interfaces.CHECK_STATUS_ISSUES:  {interfaces.RELEASE_REASON_CHECK_ISSUES, ""},
				interfaces.CHECK_STATUS_FAILED:  {interfaces.RELEASE_REASON_CHECK_FAILED, ""},
				interfaces.CHECK_STATUS_PARTIAL: {interfaces.RELEASE_REASON_CHECK_PARTIAL, interfaces.RELEASE_ACCEPT_CHECK_PARTIAL},
			} {
				check.Status = status
				expect()
				expectIndex()

				r := review()

				c := checkOf(r, interfaces.RELEASE_CHECK_CHECK)
				So(c.Passed, ShouldBeFalse)
				So(c.Reason, ShouldEqual, want[0])
				So(c.Accept, ShouldEqual, want[1])
				So(r.Blocked, ShouldEqual, want[1] == "")
			}
		})

		Convey("A check of an earlier confirmation has not been run, and shows no orphans\n", func() {
			check.Commit = "c0"
			expect()
			expectIndex()

			r := review()

			So(checkOf(r, interfaces.RELEASE_CHECK_CHECK).Reason, ShouldEqual, interfaces.RELEASE_REASON_CHECK_NOT_RUN)
			So(r.Risks.Orphans, ShouldBeEmpty)
		})

		Convey("Indexes not ready, or not readable, can be accepted\n", func() {
			Convey("Not ready\n", func() {
				index.ObjectTypes = append(index.ObjectTypes, interfaces.ProjectIndexItem{ID: "x", State: interfaces.INDEX_STATE_NOT_CONFIGURED})
				index.Counts = map[string]int{interfaces.INDEX_STATE_READY: 1, interfaces.INDEX_STATE_NOT_CONFIGURED: 1}
				expect()
				expectIndex()

				r := review()

				c := checkOf(r, interfaces.RELEASE_CHECK_INDEX)
				So(c.Reason, ShouldEqual, interfaces.RELEASE_REASON_INDEX_NOT_READY)
				So(c.Accept, ShouldEqual, interfaces.RELEASE_ACCEPT_INDEX_NOT_READY)
				So(c.Details, ShouldResemble, index.Counts)
				So(r.Accepts, ShouldResemble, []string{interfaces.RELEASE_ACCEPT_INDEX_NOT_READY})
				So(r.Blocked, ShouldBeFalse)
			})
			Convey("Not readable\n", func() {
				expect()
				drs.EXPECT().GetIndex(gomock.Any(), reviewToken, "p1", reviewOwner).Return(nil, errors.New("vega down"))

				r := review()

				c := checkOf(r, interfaces.RELEASE_CHECK_INDEX)
				So(c.Reason, ShouldEqual, interfaces.RELEASE_REASON_INDEX_UNREADABLE)
				So(c.Accept, ShouldEqual, interfaces.RELEASE_ACCEPT_INDEX_NOT_READY)
			})
		})

		Convey("Several acceptable checks are all to accept, sorted\n", func() {
			branchFiles["object_types/po.bkn"] = objectTypeBKN("po", "Purchase Orders", true)
			check.Status = interfaces.CHECK_STATUS_PARTIAL
			index.ObjectTypes[0].State = interfaces.INDEX_STATE_BUILDING
			expect()
			expectIndex()

			r := review()

			So(r.Accepts, ShouldResemble, []string{interfaces.RELEASE_ACCEPT_CHECK_PARTIAL,
				interfaces.RELEASE_ACCEPT_INDEX_NOT_READY, interfaces.RELEASE_ACCEPT_SIMILAR_NAMES})
			So(r.Blocked, ShouldBeFalse)
		})

		Convey("What the release changes or removes of the live network is a risk; other kinds are counted\n", func() {
			preview.Changes = interfaces.PathDiff{
				Added:   []string{"action_types/replenish.bkn"},
				Changed: []string{"network.bkn", "object_types/part.bkn"},
				Removed: []string{"object_types/supplier.bkn"},
			}
			preview.Capabilities = []interfaces.CapabilityPlan{{Kind: "function", ID: "b", Copy: true}, {Kind: "skill", ID: "s"}}
			delete(branchFiles, "object_types/supplier.bkn")
			expect()
			expectIndex()

			r := review()

			So(r.Changes.Added, ShouldBeEmpty)
			So(r.Changes.Changed, ShouldResemble, []interfaces.ReleaseItem{item(interfaces.RELEASE_ITEM_OBJECT_TYPE, "part", "Part")})
			// A removed item is named from main, where it still is.
			So(r.Changes.Removed, ShouldResemble, []interfaces.ReleaseItem{item(interfaces.RELEASE_ITEM_OBJECT_TYPE, "supplier", "Supplier")})
			So(r.Changes.Other, ShouldEqual, 1)
			So(r.Risks.OnlyAdditions, ShouldBeFalse)
			So(r.Risks.Existing, ShouldResemble, []interfaces.ReleaseItem{
				item(interfaces.RELEASE_ITEM_OBJECT_TYPE, "part", "Part"),
				item(interfaces.RELEASE_ITEM_OBJECT_TYPE, "supplier", "Supplier"),
			})
			So(r.Risks.Copies, ShouldEqual, 1)
			So(r.Capabilities, ShouldResemble, preview.Capabilities)
		})

		Convey("A changed definition of another kind is not an addition either\n", func() {
			preview.Changes = interfaces.PathDiff{Changed: []string{"action_types/replenish.bkn"}}
			expect()
			expectIndex()

			r := review()

			So(r.Changes.Other, ShouldEqual, 1)
			So(r.Risks.OnlyAdditions, ShouldBeFalse)
		})

		Convey("A project or a release the caller cannot read is the error it is\n", func() {
			notFound := httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Project_NotFound)
			ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(nil, notFound)
			_, err := svc.ReviewRelease(ctx, reviewToken, "p1", "1.1.0", reviewOwner)
			So(err, ShouldEqual, notFound)

			invalid := httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidVersion)
			ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil)
			ls.EXPECT().PreviewRelease(gomock.Any(), reviewToken, "supply", "project/p1", "1.1").Return(nil, invalid)
			_, err = svc.ReviewRelease(ctx, reviewToken, "p1", "1.1", reviewOwner)
			So(err, ShouldEqual, invalid)
		})

		Convey("A failure to read the draft, the check or the branch is an internal error\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil)
			ls.EXPECT().PreviewRelease(gomock.Any(), reviewToken, "supply", "project/p1", "1.1.0").Return(preview, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, errors.New("db down"))

			_, err := svc.ReviewRelease(ctx, reviewToken, "p1", "1.1.0", reviewOwner)

			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusInternalServerError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Release_InternalError)
		})
	})
}
