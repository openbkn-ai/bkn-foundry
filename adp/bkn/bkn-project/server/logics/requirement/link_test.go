// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// backendErr is what bkn-backend answers: a 404 with a code is a thing the network lacks, a plain
// 404 a feature it does not serve.
func backendErr(status int, code string) error {
	return &interfaces.BknBackendError{HTTPCode: status, BaseError: httperrors.BaseError{ErrorCode: code}}
}

// linkSpec is needsSpec with a need of every kind; n3 to n8 linked to an implementation.
func linkSpec() *interfaces.Requirement {
	req := needsSpec()
	link := func(linkType, id, boxID string) *interfaces.RequirementLogicLink {
		return &interfaces.RequirementLogicLink{Type: linkType, ID: id, BoxID: boxID, By: caller, At: 50}
	}
	req.Spec.LogicNeeds = append(req.Spec.LogicNeeds,
		&interfaces.RequirementLogicNeed{ID: "n3", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late rate",
			ObjectTypeIDs: []string{"customer"}, Link: link(interfaces.LOGIC_LINK_OBJECT_METRIC, "m1", "")},
		&interfaces.RequirementLogicNeed{ID: "n4", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Window check",
			Link: link(interfaces.LOGIC_LINK_FUNCTION, "f1", "box1")},
		&interfaces.RequirementLogicNeed{ID: "n5", Kind: interfaces.LOGIC_NEED_API, Name: "Rates",
			Link: link(interfaces.LOGIC_LINK_FUNCTION, "f2", "box1")},
		&interfaces.RequirementLogicNeed{ID: "n6", Kind: interfaces.LOGIC_NEED_SKILL, Name: "Triage",
			Link: link(interfaces.LOGIC_LINK_SKILL, "s1", "")},
		&interfaces.RequirementLogicNeed{ID: "n7", Kind: interfaces.LOGIC_NEED_MCP_TOOL, Name: "Lookup",
			Link: link(interfaces.LOGIC_LINK_MCP_TOOL, "lookup", "mcp1")},
		&interfaces.RequirementLogicNeed{ID: "n8", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Hold order",
			Link: link(interfaces.LOGIC_LINK_ACTION_TYPE, "a1", "")},
		&interfaces.RequirementLogicNeed{ID: "n9", Kind: interfaces.LOGIC_NEED_APP, Name: "Portal"},
	)
	return req
}

func Test_ListLogicNeeds(t *testing.T) {
	Convey("Test reading kinetic needs with their implementations live\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		bba := pmock.NewMockBknBackendAccess(ctrl)
		rla := pmock.NewMockReleaseAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, bba: bba, rla: rla, da: da}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		live := func() {
			rla.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "kn1", State: interfaces.RELEASE_STATE_PUBLISHED, Limit: 1}).
				Return([]*interfaces.Release{{NetworkID: "kn1"}}, nil)
			// The lineage's first release made version 1: the project created the network.
			rla.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "kn1", State: interfaces.RELEASE_STATE_PUBLISHED, Limit: releasesPage}).
				Return([]*interfaces.Release{{NetworkID: "kn1", KNVersion: 1}}, nil)
		}

		Convey("Each linked implementation is read live from the live network, once per list\n", func() {
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(linkSpec(), true, nil)
			live()
			bba.EXPECT().ListObjectTypeIDs(gomock.Any(), token, "kn1").Return([]string{"customer", "carrier"}, nil)
			bba.EXPECT().GetObjectMetric(gomock.Any(), token, "kn1", "m1").Return(&interfaces.KNObjectMetric{ID: "m1",
				Version: 3, Name: "Late rate", Description: "late share", Status: "published",
				OwnerObjectTypeID: "customer", OwnerObjectTypeName: "Customer"}, nil)
			// A function is read from the functions, an API from those bound from OpenAPI.
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_FUNCTION, "").Return(
				[]*interfaces.KNCapability{
					{CapabilityID: "f1", BoxID: "box2", Name: "Other box"},
					{CapabilityID: "f1", BoxID: "box1", Name: "Window check", Description: "d", Status: "enabled"},
					{CapabilityID: "f2", BoxID: "box1", Name: "Rates", MetadataType: "openapi"},
				}, nil).Times(1)
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_FUNCTION, "openapi").Return(
				[]*interfaces.KNCapability{{CapabilityID: "f2", BoxID: "box1", Name: "Rates", MetadataType: "openapi"}}, nil).Times(1)
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_SKILL, "").Return(
				[]*interfaces.KNCapability{{CapabilityID: "s1", Name: "Triage"}}, nil)
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_MCP_TOOL, "").Return(
				[]*interfaces.KNCapability{{CapabilityID: "lookup", BoxID: "mcp1", Name: "Lookup"}}, nil)
			bba.EXPECT().ListActionTypes(gomock.Any(), token, "kn1", "").Return(
				[]*interfaces.KNActionType{{ID: "a1", Name: "Hold order", ObjectTypeID: "sales_order"}}, nil)

			got, err := svc.ListLogicNeeds(ctx, token, "p1")

			So(err, ShouldBeNil)
			So(got.NetworkID, ShouldEqual, "kn1")
			So(got.UpdateTime, ShouldEqual, 7)
			So(got.Reason, ShouldBeEmpty)
			So(got.Needs, ShouldHaveLength, 9)
			// Only the object types the live network has are the ones to implement on.
			So(got.Needs[0].LiveObjectTypeIDs, ShouldResemble, []string{"customer"})
			So(got.Needs[1].LiveObjectTypeIDs, ShouldResemble, []string{})
			So(got.Needs[0].Implementation, ShouldBeNil)
			So(got.Needs[2].Implementation, ShouldResemble, &interfaces.LogicNeedImplementation{Available: true,
				Name: "Late rate", Description: "late share", Status: "published", Version: 3,
				OwnerObjectTypeID: "customer", OwnerObjectTypeName: "Customer"})
			So(got.Needs[3].Implementation, ShouldResemble, &interfaces.LogicNeedImplementation{Available: true,
				Name: "Window check", Description: "d", Status: "enabled"})
			So(got.Needs[4].Implementation.Name, ShouldEqual, "Rates")
			So(got.Needs[5].Implementation.Name, ShouldEqual, "Triage")
			So(got.Needs[6].Implementation.Name, ShouldEqual, "Lookup")
			So(got.Needs[7].Implementation, ShouldResemble, &interfaces.LogicNeedImplementation{Available: true,
				Name: "Hold order", OwnerObjectTypeID: "sales_order"})
			So(got.Needs[8].Implementation, ShouldBeNil)
			// A network the project created lists nothing as already there.
			So(got.NetworkItems, ShouldBeEmpty)
		})

		Convey("An implementation gone, forbidden or not answered says so; one failed list is read once\n", func() {
			req := needsSpec()
			req.Spec.LogicNeeds = []*interfaces.RequirementLogicNeed{
				{ID: "n1", Kind: interfaces.LOGIC_NEED_METRIC, Link: &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_OBJECT_METRIC, ID: "gone"}},
				{ID: "n2", Kind: interfaces.LOGIC_NEED_METRIC, Link: &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_OBJECT_METRIC, ID: "secret"}},
				{ID: "n3", Kind: interfaces.LOGIC_NEED_METRIC, Link: &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_OBJECT_METRIC, ID: "nofeature"}},
				{ID: "n4", Kind: interfaces.LOGIC_NEED_SKILL, Link: &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_SKILL, ID: "s9"}},
				{ID: "n5", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Link: &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_ACTION_TYPE, ID: "a1"}},
				{ID: "n6", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Link: &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_ACTION_TYPE, ID: "a2"}},
			}
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(req, true, nil)
			// Before the first release the live network is the lineage's own.
			rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			bba.EXPECT().ListObjectTypeIDs(gomock.Any(), token, "kn1").Return([]string{}, nil)
			bba.EXPECT().GetObjectMetric(gomock.Any(), token, "kn1", "gone").Return(nil, backendErr(http.StatusNotFound, "BknBackend.ObjectMetric.NotFound"))
			bba.EXPECT().GetObjectMetric(gomock.Any(), token, "kn1", "secret").Return(nil, backendErr(http.StatusForbidden, "BknBackend.Forbidden"))
			bba.EXPECT().GetObjectMetric(gomock.Any(), token, "kn1", "nofeature").Return(nil, backendErr(http.StatusNotFound, ""))
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_SKILL, "").Return(
				[]*interfaces.KNCapability{{CapabilityID: "s1"}}, nil)
			bba.EXPECT().ListActionTypes(gomock.Any(), token, "kn1", "").Return(nil, errors.New("connection refused")).Times(1)

			got, err := svc.ListLogicNeeds(ctx, token, "p1")

			So(err, ShouldBeNil)
			So(got.NetworkID, ShouldEqual, "kn1")
			reasons := []string{}
			for _, n := range got.Needs {
				So(n.Implementation.Available, ShouldBeFalse)
				reasons = append(reasons, n.Implementation.Reason)
			}
			So(reasons, ShouldResemble, []string{interfaces.LOGIC_IMPLEMENTATION_MISSING, interfaces.LOGIC_IMPLEMENTATION_FORBIDDEN,
				interfaces.LOGIC_IMPLEMENTATION_UNAVAILABLE, interfaces.LOGIC_IMPLEMENTATION_MISSING,
				interfaces.LOGIC_IMPLEMENTATION_UNAVAILABLE, interfaces.LOGIC_IMPLEMENTATION_UNAVAILABLE})
		})

		Convey("Object types the live network does not answer leave no live object types and say why\n", func() {
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(needsSpec(), true, nil)
			live()
			bba.EXPECT().ListObjectTypeIDs(gomock.Any(), token, "kn1").Return(nil, backendErr(http.StatusForbidden, "x"))

			got, err := svc.ListLogicNeeds(ctx, token, "p1")

			So(err, ShouldBeNil)
			So(got.Reason, ShouldEqual, interfaces.LOGIC_TARGET_UNAVAILABLE)
			So(got.Needs[0].LiveObjectTypeIDs, ShouldResemble, []string{})
		})

		Convey("A project without a specification has no needs, which is no error; a failed release read is one\n", func() {
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil)
			live()
			bba.EXPECT().ListObjectTypeIDs(gomock.Any(), token, "kn1").Return([]string{}, nil)
			got, err := svc.ListLogicNeeds(ctx, token, "p1")
			So(err, ShouldBeNil)
			So(got.Needs, ShouldBeEmpty)
			So(got.Released, ShouldBeTrue)
			So(got.Live, ShouldBeTrue)
			So(got.NetworkID, ShouldEqual, "kn1")

			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(needsSpec(), true, nil)
			rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))
			_, err = svc.ListLogicNeeds(ctx, token, "p1")
			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Requirement_InternalError)
		})
	})
}

func Test_ListLogicNeedsBeforeRelease(t *testing.T) {
	Convey("Before the first release nothing is live, and that is no failure to read\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		bba := pmock.NewMockBknBackendAccess(ctrl)
		rla := pmock.NewMockReleaseAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, bba: bba, rla: rla, da: da}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{
			Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller}}, nil)
		rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
		// The network the project started from is not there yet.
		bba.EXPECT().ListObjectTypeIDs(gomock.Any(), token, "kn1").Return(nil, backendErr(http.StatusNotFound, "BknBackend.KnowledgeNetwork.NotFound"))
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(needsSpec(), true, nil)
		// Of the draft's adopted object types, sales_order and customer are joined by no relation.
		draft := testDraft()
		draft.Items.RelationTypes = nil
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)

		got, err := svc.ListLogicNeeds(ctx, token, "p1")

		So(err, ShouldBeNil)
		So(got.Released, ShouldBeFalse)
		So(got.Live, ShouldBeFalse)
		So(got.Reason, ShouldBeEmpty)
		So(got.NetworkID, ShouldEqual, "kn1")
		So(got.Needs[0].LiveObjectTypeIDs, ShouldBeEmpty)
		So(got.Needs[0].Unconnected, ShouldResemble, []interfaces.ScenarioUnconnected{{From: "sales_order", To: "customer"}})
		So(got.Needs[1].Unconnected, ShouldBeEmpty)
	})

	Convey("Before the first release, a network the project started from that runs a version is live\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		bba := pmock.NewMockBknBackendAccess(ctrl)
		rla := pmock.NewMockReleaseAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		kva := pmock.NewMockKNVersionAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, bba: bba, rla: rla, da: da, kva: kva}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{
			Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller}}, nil).Times(2)
		rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)
		bba.EXPECT().ListObjectTypeIDs(gomock.Any(), token, "kn1").Return([]string{"sales_order"}, nil).Times(2)
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil).Times(2)

		kva.EXPECT().GetVersioning(gomock.Any(), token, "kn1").Return(&interfaces.KNVersioning{Enabled: true, RunningVersion: 1}, nil)
		bba.EXPECT().ListActionTypes(gomock.Any(), token, "kn1", "").Return(nil, nil)
		bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", gomock.Any(), "").Return(nil, nil).Times(2)
		bba.EXPECT().ListObjectMetrics(gomock.Any(), token, "kn1", "").Return(nil, nil)
		got, err := svc.ListLogicNeeds(ctx, token, "p1")
		So(err, ShouldBeNil)
		So(got.Released, ShouldBeFalse)
		So(got.Live, ShouldBeTrue)
		So(got.NetworkItems, ShouldBeEmpty)

		// One whose versions cannot be read is taken not to run one.
		kva.EXPECT().GetVersioning(gomock.Any(), token, "kn1").Return(nil, errors.New("down"))
		got, err = svc.ListLogicNeeds(ctx, token, "p1")
		So(err, ShouldBeNil)
		So(got.Live, ShouldBeFalse)
	})

	Convey("Before the first release, a network that fails to answer is still a failure to read\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		bba := pmock.NewMockBknBackendAccess(ctrl)
		rla := pmock.NewMockReleaseAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, bba: bba, rla: rla, da: da}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{
			Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller}}, nil)
		rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
		bba.EXPECT().ListObjectTypeIDs(gomock.Any(), token, "kn1").Return(nil, backendErr(http.StatusBadGateway, ""))
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(needsSpec(), true, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)

		got, err := svc.ListLogicNeeds(context.Background(), token, "p1")

		So(err, ShouldBeNil)
		So(got.Released, ShouldBeFalse)
		So(got.Reason, ShouldEqual, interfaces.LOGIC_TARGET_UNAVAILABLE)
	})
}

func Test_LogicNeedCandidates(t *testing.T) {
	Convey("Test the candidates of a kinetic need\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		bba := pmock.NewMockBknBackendAccess(ctrl)
		rla := pmock.NewMockReleaseAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, bba: bba, rla: rla}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(linkSpec(), true, nil).AnyTimes()
		rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return([]*interfaces.Release{{NetworkID: "kn1"}}, nil).AnyTimes()
		idsOf := func(c *interfaces.LogicNeedCandidates) []string {
			out := []string{}
			for _, x := range c.Candidates {
				out = append(out, x.ID)
			}
			return out
		}

		Convey("Metrics of the same name come first, then similar names, then those of the need's object types\n", func() {
			// n1 is "Late rate" on sales_order and customer.
			bba.EXPECT().ListObjectMetrics(gomock.Any(), token, "kn1", "").Return([]*interfaces.KNObjectMetric{
				{ID: "other_type", Name: "Order count", OwnerObjectTypeID: "customer"},
				{ID: "unrelated", Name: "Stock level", OwnerObjectTypeID: "warehouse"},
				{ID: "similar", Name: "Late rate daily", OwnerObjectTypeID: "warehouse"},
				{ID: "same", Name: " late_RATE ", OwnerObjectTypeID: "warehouse"},
				{ID: "same_on_type", Name: "Late-Rate", OwnerObjectTypeID: "sales_order", Description: "d"},
			}, nil)

			got, err := svc.LogicNeedCandidates(ctx, token, "p1", "n1")

			So(err, ShouldBeNil)
			So(got.NetworkID, ShouldEqual, "kn1")
			So(got.Reason, ShouldBeEmpty)
			So(idsOf(got), ShouldResemble, []string{"same_on_type", "same", "similar", "other_type"})
			So(got.Candidates[0], ShouldResemble, &interfaces.LogicNeedCandidate{Type: interfaces.LOGIC_LINK_OBJECT_METRIC,
				ID: "same_on_type", Name: "Late-Rate", Description: "d", OwnerObjectTypeID: "sales_order",
				Reasons: []string{interfaces.LOGIC_CANDIDATE_SAME_NAME, interfaces.LOGIC_CANDIDATE_SAME_OBJECT}})
			So(got.Candidates[2].Reasons, ShouldResemble, []string{interfaces.LOGIC_CANDIDATE_SIMILAR})
			So(got.Candidates[3].Reasons, ShouldResemble, []string{interfaces.LOGIC_CANDIDATE_SAME_OBJECT})
		})

		Convey("At most twenty are offered\n", func() {
			metrics := []*interfaces.KNObjectMetric{}
			for i := range 25 {
				metrics = append(metrics, &interfaces.KNObjectMetric{ID: fmt.Sprintf("m%d", i), Name: fmt.Sprintf("x%d", i),
					OwnerObjectTypeID: "customer"})
			}
			bba.EXPECT().ListObjectMetrics(gomock.Any(), token, "kn1", "").Return(metrics, nil)

			got, err := svc.LogicNeedCandidates(ctx, token, "p1", "n1")

			So(err, ShouldBeNil)
			So(got.Candidates, ShouldHaveLength, maxCandidates)
		})

		Convey("An API need is offered the functions bound from OpenAPI descriptions\n", func() {
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_FUNCTION, "openapi").Return(
				[]*interfaces.KNCapability{{CapabilityID: "f2", BoxID: "box1", Name: "Rates", Description: "d"}}, nil)

			got, err := svc.LogicNeedCandidates(ctx, token, "p1", "n5")

			So(err, ShouldBeNil)
			So(got.Candidates, ShouldResemble, []*interfaces.LogicNeedCandidate{{Type: interfaces.LOGIC_LINK_FUNCTION,
				ID: "f2", BoxID: "box1", Name: "Rates", Description: "d", Reasons: []string{interfaces.LOGIC_CANDIDATE_SAME_NAME}}})
		})

		Convey("A function need is offered every bound function\n", func() {
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_FUNCTION, "").Return(
				[]*interfaces.KNCapability{{CapabilityID: "f1", BoxID: "box1", Name: "window check"}}, nil)

			got, err := svc.LogicNeedCandidates(ctx, token, "p1", "n4")

			So(err, ShouldBeNil)
			So(idsOf(got), ShouldResemble, []string{"f1"})
		})

		Convey("Action types are offered by name and by the object type they act on\n", func() {
			req := linkSpec()
			req.Spec.LogicNeeds[7].ObjectTypeIDs = []string{"sales_order"}
			ra2 := pmock.NewMockRequirementAccess(ctrl)
			svc.ra = ra2
			ra2.EXPECT().GetRequirement(gomock.Any(), "p1").Return(req, true, nil)
			bba.EXPECT().ListActionTypes(gomock.Any(), token, "kn1", "").Return([]*interfaces.KNActionType{
				{ID: "a2", Name: "Release order", ObjectTypeID: "sales_order"},
				{ID: "a3", Name: "Ship", ObjectTypeID: "carrier"},
				{ID: "a1", Name: "Hold order", ObjectTypeID: "carrier"},
			}, nil)

			got, err := svc.LogicNeedCandidates(ctx, token, "p1", "n8")

			So(err, ShouldBeNil)
			So(idsOf(got), ShouldResemble, []string{"a1", "a2"})
			So(got.Candidates[1].OwnerObjectTypeID, ShouldEqual, "sales_order")
		})

		Convey("A list the network does not answer leaves no candidates and says why\n", func() {
			bba.EXPECT().ListObjectMetrics(gomock.Any(), token, "kn1", "").Return(nil, backendErr(http.StatusNotFound, ""))

			got, err := svc.LogicNeedCandidates(ctx, token, "p1", "n1")

			So(err, ShouldBeNil)
			So(got.Reason, ShouldEqual, interfaces.LOGIC_IMPLEMENTATION_UNAVAILABLE)
			So(got.Candidates, ShouldResemble, []*interfaces.LogicNeedCandidate{})

			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_SKILL, "").Return(nil, backendErr(http.StatusForbidden, "x"))
			got, err = svc.LogicNeedCandidates(ctx, token, "p1", "n6")
			So(err, ShouldBeNil)
			So(got.Reason, ShouldEqual, interfaces.LOGIC_IMPLEMENTATION_FORBIDDEN)
		})

		Convey("An application need has none, and an unknown need is not found\n", func() {
			got, err := svc.LogicNeedCandidates(ctx, token, "p1", "n9")
			So(err, ShouldBeNil)
			So(got.Candidates, ShouldResemble, []*interfaces.LogicNeedCandidate{})

			_, err = svc.LogicNeedCandidates(ctx, token, "p1", "n99")
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)
		})
	})
}

func Test_LinkLogicNeed(t *testing.T) {
	Convey("Test linking a kinetic need to its implementation\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		bba := pmock.NewMockBknBackendAccess(ctrl)
		rla := pmock.NewMockReleaseAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, bba: bba, rla: rla, now: func() int64 { return 100 }}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").DoAndReturn(
			func(context.Context, string, string) (*interfaces.ProjectEntry, error) { return project, nil }).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(kineticDraft(), true, nil).AnyTimes()
		rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

		var saved *interfaces.Requirement
		// Linking reads the specification to check the need before the network, then again to write.
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").DoAndReturn(
			func(context.Context, string) (*interfaces.Requirement, bool, error) { return linkSpec(), true, nil }).AnyTimes()
		expectRead := func() {}
		expectSave := func() {
			ra.EXPECT().UpdateRequirement(gomock.Any(), gomock.Any(), int64(7)).DoAndReturn(
				func(_ context.Context, req *interfaces.Requirement, _ int64) (bool, error) {
					saved = req
					return true, nil
				})
		}
		metric := func(id string, m *interfaces.KNObjectMetric, err error) {
			bba.EXPECT().GetObjectMetric(gomock.Any(), token, "kn1", id).Return(m, err)
		}
		edit := func(linkType, id, boxID string) interfaces.RequirementLogicLinkEdit {
			return interfaces.RequirementLogicLinkEdit{UpdateTime: 7, Type: linkType, ID: id, BoxID: boxID}
		}

		Convey("A need is linked to an implementation the live network has, by whom and when, and is accepted\n", func() {
			metric("m2", &interfaces.KNObjectMetric{ID: "m2", Name: "Late rate"}, nil)
			expectRead()
			expectSave()

			got, err := svc.LinkLogicNeed(ctx, token, "p1", "n1", edit(interfaces.LOGIC_LINK_OBJECT_METRIC, " m2 ", ""), caller)

			So(err, ShouldBeNil)
			So(got, ShouldEqual, saved)
			n := saved.Spec.LogicNeeds[0]
			So(n.Link, ShouldResemble, &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_OBJECT_METRIC, ID: "m2",
				By: caller, At: 100})
			So(n.Accepted, ShouldBeTrue)
			So(saved.UpdateTime, ShouldEqual, 100)
		})

		Convey("A function is linked by its toolbox\n", func() {
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_FUNCTION, "").Return(
				[]*interfaces.KNCapability{{CapabilityID: "f9", BoxID: "box2"}}, nil)
			expectRead()
			expectSave()

			_, err := svc.LinkLogicNeed(ctx, token, "p1", "n2", edit(interfaces.LOGIC_LINK_FUNCTION, "f9", "box2"), caller)

			So(err, ShouldBeNil)
			So(saved.Spec.LogicNeeds[1].Link.BoxID, ShouldEqual, "box2")
		})

		Convey("An implementation of another kind than the need's is refused, before the network is read\n", func() {
			expectRead()

			_, err := svc.LinkLogicNeed(ctx, token, "p1", "n1", edit(interfaces.LOGIC_LINK_SKILL, "s1", ""), caller)

			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
		})

		Convey("An application need links to nothing; an unknown need is not found, with no read of the network\n", func() {
			_, err := svc.LinkLogicNeed(ctx, token, "p1", "n9", edit(interfaces.LOGIC_LINK_SKILL, "s1", ""), caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)

			_, err = svc.LinkLogicNeed(ctx, token, "p1", "n99", edit(interfaces.LOGIC_LINK_OBJECT_METRIC, "m1", ""), caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)
		})

		Convey("An API need links only to a function bound from OpenAPI\n", func() {
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_FUNCTION, "openapi").Return(
				[]*interfaces.KNCapability{{CapabilityID: "f2", BoxID: "box1", MetadataType: "openapi"}}, nil)

			_, err := svc.LinkLogicNeed(ctx, token, "p1", "n5", edit(interfaces.LOGIC_LINK_FUNCTION, "f9", "box1"), caller)

			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
		})

		Convey("A link without an id, or a function or MCP tool without its box, is refused before any read\n", func() {
			for _, e := range []interfaces.RequirementLogicLinkEdit{
				edit(interfaces.LOGIC_LINK_OBJECT_METRIC, "  ", ""),
				edit(interfaces.LOGIC_LINK_FUNCTION, "f1", " "),
				edit(interfaces.LOGIC_LINK_MCP_TOOL, "lookup", ""),
			} {
				_, err := svc.LinkLogicNeed(ctx, token, "p1", "n1", e, caller)
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
			}
		})

		Convey("An implementation gone or forbidden is refused; one not answered is unavailable\n", func() {
			metric("gone", nil, backendErr(http.StatusNotFound, "BknBackend.ObjectMetric.NotFound"))
			_, err := svc.LinkLogicNeed(ctx, token, "p1", "n1", edit(interfaces.LOGIC_LINK_OBJECT_METRIC, "gone", ""), caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)

			metric("secret", nil, backendErr(http.StatusForbidden, "BknBackend.Forbidden"))
			_, err = svc.LinkLogicNeed(ctx, token, "p1", "n1", edit(interfaces.LOGIC_LINK_OBJECT_METRIC, "secret", ""), caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)

			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_SKILL, "").Return(
				[]*interfaces.KNCapability{{CapabilityID: "s1"}}, nil)
			_, err = svc.LinkLogicNeed(ctx, token, "p1", "n6", edit(interfaces.LOGIC_LINK_SKILL, "s9", ""), caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)

			metric("m1", nil, backendErr(http.StatusNotFound, ""))
			_, err = svc.LinkLogicNeed(ctx, token, "p1", "n1", edit(interfaces.LOGIC_LINK_OBJECT_METRIC, "m1", ""), caller)
			shouldBeHTTPError(err, http.StatusServiceUnavailable, perrors.BknProject_Requirement_ImplementationUnavailable)

			bba.EXPECT().ListActionTypes(gomock.Any(), token, "kn1", "").Return(nil, errors.New("connection refused"))
			_, err = svc.LinkLogicNeed(ctx, token, "p1", "n8", edit(interfaces.LOGIC_LINK_ACTION_TYPE, "a1", ""), caller)
			shouldBeHTTPError(err, http.StatusServiceUnavailable, perrors.BknProject_Requirement_ImplementationUnavailable)
		})

		Convey("Only the owner links, over the specification as read\n", func() {
			project = &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1",
				Owner: interfaces.AccountInfo{ID: "u2", Type: "user"}}}
			_, err := svc.LinkLogicNeed(ctx, token, "p1", "n1", edit(interfaces.LOGIC_LINK_OBJECT_METRIC, "m1", ""), caller)
			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Requirement_NotOwner)

			project = &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller}}
			metric("m1", &interfaces.KNObjectMetric{ID: "m1"}, nil)
			expectRead()
			stale := edit(interfaces.LOGIC_LINK_OBJECT_METRIC, "m1", "")
			stale.UpdateTime = 6
			_, err = svc.LinkLogicNeed(ctx, token, "p1", "n1", stale, caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Stale)
		})

		Convey("Unlinking removes the link and keeps the need\n", func() {
			expectRead()
			expectSave()

			_, err := svc.UnlinkLogicNeed(ctx, token, "p1", "n3", 7, caller)

			So(err, ShouldBeNil)
			So(saved.Spec.LogicNeeds[2].ID, ShouldEqual, "n3")
			So(saved.Spec.LogicNeeds[2].Link, ShouldBeNil)
			So(saved.Spec.LogicNeeds, ShouldHaveLength, 9)

			expectRead()
			_, err = svc.UnlinkLogicNeed(ctx, token, "p1", "n99", 7, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)

			_, err = svc.UnlinkLogicNeed(ctx, token, "p1", "n3", 0, caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_MissingUpdateTime)
		})
	})
}

func Test_Squeezed(t *testing.T) {
	Convey("Names compare without case, any space, underscores or hyphens\n", t, func() {
		So(squeezed(" Late_Rate-x　y\tz "), ShouldEqual, "lateratexyz")
	})
}

func Test_ListLogicNeedsNetworkItems(t *testing.T) {
	Convey("A project started on a running network lists what the network has, with the needs each covers\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		bba := pmock.NewMockBknBackendAccess(ctrl)
		rla := pmock.NewMockReleaseAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		kva := pmock.NewMockKNVersionAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, bba: bba, rla: rla, da: da, kva: kva}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{
			Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller}}, nil).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		bba.EXPECT().ListObjectTypeIDs(gomock.Any(), token, "kn1").Return([]string{"sales_order"}, nil).AnyTimes()
		lists := func() {
			bba.EXPECT().ListActionTypes(gomock.Any(), token, "kn1", "").Return(
				[]*interfaces.KNActionType{{ID: "a1", Name: "Hold order", ObjectTypeID: "sales_order"}}, nil)
			// The network mounts f1, an API and s1; f7 is a tool an action type runs that it does not mount.
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_FUNCTION, "").Return(
				[]*interfaces.KNCapability{{ID: "b1", CapabilityID: "f1", BoxID: "box1", Name: "Window check", Description: "d"},
					{CapabilityID: "f7", BoxID: "box1", Name: "Hold"},
					{ID: "b3", CapabilityID: "f8", BoxID: "box9", Name: "Rates", MetadataType: "openapi"}}, nil)
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_SKILL, "").Return(
				[]*interfaces.KNCapability{{ID: "b2", CapabilityID: "s1", Name: "Triage"}}, nil)
		}

		Convey("Before the first release, with the needs linked to them; metrics the edition lacks are none\n", func() {
			rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			kva.EXPECT().GetVersioning(gomock.Any(), token, "kn1").Return(&interfaces.KNVersioning{Enabled: true, RunningVersion: 2}, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(linkSpec(), true, nil)
			lists()
			bba.EXPECT().ListObjectMetrics(gomock.Any(), token, "kn1", "").Return(nil, backendErr(http.StatusNotFound, ""))
			// The needs' own implementations are read from the same lists.
			bba.EXPECT().GetObjectMetric(gomock.Any(), token, "kn1", "m1").Return(nil, backendErr(http.StatusNotFound, ""))
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_FUNCTION, "openapi").Return(nil, nil)
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_MCP_TOOL, "").Return(nil, nil)

			got, err := svc.ListLogicNeeds(ctx, token, "p1")

			So(err, ShouldBeNil)
			So(got.Live, ShouldBeTrue)
			So(got.NetworkItemsReason, ShouldBeEmpty)
			So(got.NetworkItems, ShouldResemble, []*interfaces.LogicNetworkItem{
				{Type: interfaces.LOGIC_LINK_ACTION_TYPE, Kind: interfaces.LOGIC_NEED_ACTION_TYPE, ID: "a1", Name: "Hold order",
					OwnerObjectTypeID: "sales_order", NeedIDs: []string{"n8"}},
				{Type: interfaces.LOGIC_LINK_FUNCTION, Kind: interfaces.LOGIC_NEED_FUNCTION, ID: "f1", BoxID: "box1", Name: "Window check",
					Description: "d", NeedIDs: []string{"n4"}},
				// An API is a function bound from an OpenAPI description, listed as one.
				{Type: interfaces.LOGIC_LINK_FUNCTION, Kind: interfaces.LOGIC_NEED_API, ID: "f8", BoxID: "box9", Name: "Rates",
					NeedIDs: []string{}},
				{Type: interfaces.LOGIC_LINK_SKILL, Kind: interfaces.LOGIC_NEED_SKILL, ID: "s1", Name: "Triage", NeedIDs: []string{"n6"}},
			})
		})

		Convey("Without a specification yet; a list not answered says why and the rest is listed\n", func() {
			rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			kva.EXPECT().GetVersioning(gomock.Any(), token, "kn1").Return(&interfaces.KNVersioning{Enabled: true, RunningVersion: 2}, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil)
			bba.EXPECT().ListActionTypes(gomock.Any(), token, "kn1", "").Return(nil, backendErr(http.StatusForbidden, "x"))
			bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", gomock.Any(), "").Return(nil, nil).Times(2)
			bba.EXPECT().ListObjectMetrics(gomock.Any(), token, "kn1", "").Return([]*interfaces.KNObjectMetric{
				{ID: "m1", Name: "Late rate", OwnerObjectTypeID: "sales_order", OwnerObjectTypeName: "Sales order"}}, nil)

			got, err := svc.ListLogicNeeds(ctx, token, "p1")

			So(err, ShouldBeNil)
			So(got.NetworkItemsReason, ShouldEqual, interfaces.LOGIC_IMPLEMENTATION_FORBIDDEN)
			So(got.NetworkItems, ShouldResemble, []*interfaces.LogicNetworkItem{{Type: interfaces.LOGIC_LINK_OBJECT_METRIC, Kind: interfaces.LOGIC_NEED_METRIC, ID: "m1",
				Name: "Late rate", OwnerObjectTypeID: "sales_order", OwnerObjectTypeName: "Sales order", NeedIDs: []string{}}})
		})

		Convey("After the project's release, when its first release built on versions the network had\n", func() {
			rla.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "kn1", State: interfaces.RELEASE_STATE_PUBLISHED, Limit: 1}).
				Return([]*interfaces.Release{{ID: "r2"}}, nil)
			page := make([]*interfaces.Release, releasesPage)
			for i := range page {
				page[i] = &interfaces.Release{ID: fmt.Sprintf("r%d", 200-i), CreateTime: int64(200 - i), KNVersion: 200 - i}
			}
			rla.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "kn1", State: interfaces.RELEASE_STATE_PUBLISHED, Limit: releasesPage}).
				Return(page, nil)
			rla.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "kn1", State: interfaces.RELEASE_STATE_PUBLISHED, Limit: releasesPage,
				Cursor: &interfaces.ReleaseCursor{CreateTime: 101, ID: "r101"}}).Return([]*interfaces.Release{{ID: "r1", KNVersion: 4}}, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil)
			lists()
			bba.EXPECT().ListObjectMetrics(gomock.Any(), token, "kn1", "").Return(nil, nil)

			got, err := svc.ListLogicNeeds(ctx, token, "p1")

			So(err, ShouldBeNil)
			So(got.Released, ShouldBeTrue)
			So(got.NetworkItems, ShouldHaveLength, 4)
		})
	})
}
