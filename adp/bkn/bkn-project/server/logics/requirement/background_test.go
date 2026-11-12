// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

const networkSkill = "---\nname: supply\ndescription: How to read the network\n---\n\n# Supply chain\n\n" +
	"Today is the business date of the latest snapshot.\n"

func Test_NetworkBackground(t *testing.T) {
	Convey("Test the network's own background\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		bba := pmock.NewMockBknBackendAccess(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		svc := &requirementService{bba: bba, gs: gs}
		ctx := context.Background()
		entry := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Branch: "project/p1"}}

		Convey("an existing network gives its description and SKILL.md without its frontmatter\n", func() {
			bba.EXPECT().GetKN(gomock.Any(), token, "kn1").Return(&interfaces.KnowledgeNetwork{ID: "kn1",
				Name: "Supply", Comment: "Covers orders and deliveries."}, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{
				"SKILL.md": []byte(networkSkill), "network.bkn": []byte("---\ntype: knowledge_network\n---\n")}, nil)

			doc, err := svc.networkBackground(ctx, token, entry)
			So(err, ShouldBeNil)
			So(doc.ID, ShouldEqual, interfaces.REQUIREMENT_NETWORK_DOCUMENT_ID)
			So(doc.Name, ShouldEqual, "Supply")
			So(doc.Content, ShouldEqual, "Covers orders and deliveries.\n\n# Supply chain\n\n"+
				"Today is the business date of the latest snapshot.")
			So(doc.Content, ShouldNotContainSubstring, "description: How to read")
			So(doc.Blocks, ShouldNotBeEmpty)
			So(doc.SHA256, ShouldNotBeEmpty)
		})

		Convey("a new network, without a description or SKILL.md, has none\n", func() {
			bba.EXPECT().GetKN(gomock.Any(), token, "kn1").Return(&interfaces.KnowledgeNetwork{ID: "kn1", Name: "New"}, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{
				"network.bkn": []byte("---\ntype: knowledge_network\n---\n")}, nil)

			doc, err := svc.networkBackground(ctx, token, entry)
			So(err, ShouldBeNil)
			So(doc, ShouldBeNil)
		})

		Convey("a long background is cut\n", func() {
			bba.EXPECT().GetKN(gomock.Any(), token, "kn1").Return(&interfaces.KnowledgeNetwork{ID: "kn1",
				Comment: strings.Repeat("a", maxBackgroundRunes+10)}, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{}, nil)

			doc, err := svc.networkBackground(ctx, token, entry)
			So(err, ShouldBeNil)
			So(len([]rune(doc.Content)), ShouldEqual, maxBackgroundRunes)
		})

		Convey("a background that cannot be read leaves a run to the documents\n", func() {
			bba.EXPECT().GetKN(gomock.Any(), token, "kn1").Return(nil, errors.New("boom"))
			docs := []*interfaces.RequirementDocument{{ID: "d1"}}

			So(svc.withBackground(ctx, token, entry, docs), ShouldResemble, docs)
		})
	})
}

func Test_WithoutFrontmatter(t *testing.T) {
	Convey("Test leaving out a Markdown file's frontmatter\n", t, func() {
		So(withoutFrontmatter("---\nname: a\n---\nbody\n"), ShouldEqual, "body\n")
		So(withoutFrontmatter("body only"), ShouldEqual, "body only")
		So(withoutFrontmatter("---\nnot closed\n"), ShouldEqual, "---\nnot closed\n")
	})
}

func Test_GetNetworkDocument(t *testing.T) {
	Convey("Test reading the network's background as a document of the project\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		bba := pmock.NewMockBknBackendAccess(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		svc := &requirementService{ps: ps, ra: ra, bba: bba, gs: gs}
		ctx := context.Background()
		entry := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Branch: "project/p1"}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(entry, nil)

		Convey("it is read where it is kept, never from the uploads\n", func() {
			bba.EXPECT().GetKN(gomock.Any(), token, "kn1").Return(&interfaces.KnowledgeNetwork{ID: "kn1"}, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{
				"SKILL.md": []byte(networkSkill)}, nil)

			doc, err := svc.GetDocument(ctx, token, "p1", interfaces.REQUIREMENT_NETWORK_DOCUMENT_ID)
			So(err, ShouldBeNil)
			So(doc.Content, ShouldStartWith, "# Supply chain")
		})

		Convey("a network that cannot be read fails the read\n", func() {
			bba.EXPECT().GetKN(gomock.Any(), token, "kn1").Return(nil, errors.New("boom"))

			_, err := svc.GetDocument(ctx, token, "p1", interfaces.REQUIREMENT_NETWORK_DOCUMENT_ID)
			So(err, ShouldNotBeNil)
		})
	})

	Convey("A network without a background has no such document\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		bba := pmock.NewMockBknBackendAccess(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		svc := &requirementService{ps: ps, bba: bba, gs: gs}
		entry := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Branch: "project/p1"}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(entry, nil)
		bba.EXPECT().GetKN(gomock.Any(), token, "kn1").Return(&interfaces.KnowledgeNetwork{ID: "kn1"}, nil)
		gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{}, nil)

		_, err := svc.GetDocument(context.Background(), token, "p1", interfaces.REQUIREMENT_NETWORK_DOCUMENT_ID)
		var httpErr *httperrors.HTTPError
		So(errors.As(err, &httpErr), ShouldBeTrue)
		So(httpErr.HTTPCode, ShouldEqual, http.StatusNotFound)
	})
}

func Test_ExtractConceptsCitesNetworkBackground(t *testing.T) {
	Convey("Test the concepts run on a project of an existing network\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		bba := pmock.NewMockBknBackendAccess(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, ba: ba, bba: bba, gs: gs,
			now: func() int64 { return 1000 }, newID: func() string { return "run1" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, KNID: "kn1",
			Branch: "project/p1", CatalogIDs: []string{"c1"}, NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US}}
		docs := testDocuments()
		st := &runStore{}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
		st.expect(ra)
		va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return([]*interfaces.VegaTable{}, nil)
		bba.EXPECT().GetKN(gomock.Any(), token, "kn1").Return(&interfaces.KnowledgeNetwork{ID: "kn1", Name: "Supply"}, nil)
		gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{
			"SKILL.md": []byte(networkSkill)}, nil)
		output := `{"concepts":[{"id":"a","term":"Business date","category":"property",
			"citations":[{"document_id":"network","quote":"the business date of the latest snapshot"}]}]}`
		ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
				var msg conceptsMessage
				So(json.Unmarshal([]byte(req.Message[strings.Index(req.Message, "{"):]), &msg), ShouldBeNil)
				// The documents, then the network's background.
				So(msg.Documents, ShouldHaveLength, len(docs)+1)
				So(msg.Documents[len(docs)].ID, ShouldEqual, interfaces.REQUIREMENT_NETWORK_DOCUMENT_ID)
				return &interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}, nil
			})
		ra.EXPECT().SuggestDocumentType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(true, nil).AnyTimes()

		_, err := svc.ExtractConcepts(ctx, token, "p1", caller, false)
		So(err, ShouldBeNil)
		spec := st.req.Spec
		So(spec.Concepts, ShouldHaveLength, 1)
		So(spec.Concepts[0].Citations, ShouldHaveLength, 1)
		So(spec.Concepts[0].Citations[0].DocumentID, ShouldEqual, interfaces.REQUIREMENT_NETWORK_DOCUMENT_ID)
		// The run records the uploaded documents only, so the background never makes it stale.
		So(spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS].Inputs.Documents, ShouldHaveLength, len(docs))
	})
}
