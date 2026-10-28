// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/gitstore"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

const token = "Bearer test-token"

var (
	author = interfaces.GitAuthor{Name: "u1", Email: "u1@bkn-project"}
	// gitRoot holds the repositories of the real git store, one per test lineage.
	gitRoot string
	realGS  interfaces.GitStore
	seq     int
)

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "bkn-project-lineage-")
	if err != nil {
		panic(err)
	}
	gitRoot = root
	realGS = gitstore.NewGitStore(&common.AppSetting{GitStoreSetting: common.GitStoreSetting{RootDir: root}})
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

// newLineageID gives every test run its own repository in the shared store.
func newLineageID() string {
	seq++
	return fmt.Sprintf("l%d", seq)
}

const networkText = `---
type: knowledge_network
id: supply
name: Supply chain
---

# Supply chain

Parts and the suppliers that deliver them.
`

func objectType(id, name, desc string) []byte {
	return []byte(fmt.Sprintf(`---
type: object_type
id: %s
name: %s
---

## ObjectType: %s

%s

### Data Source

| Type | ID | Name |
|------|----|------|
| resource | view_%s | %s_view |

### Data Properties

| Name | Display Name | Type | Description | Mapped Field |
|------|--------------|------|-------------|--------------|
| id | id | string | Identifier | id |

### Keys

Primary Keys: id
Display Key: id
Incremental Key:
`, id, name, name, desc, id, id))
}

func definitions(partDesc, supplierDesc string) map[string][]byte {
	return map[string][]byte{
		"network.bkn":               []byte(networkText),
		"object_types/part.bkn":     objectType("part", "Part", partDesc),
		"object_types/supplier.bkn": objectType("supplier", "Supplier", supplierDesc),
	}
}

func httpCode(err error) (int, string) {
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		return 0, ""
	}
	return httpErr.HTTPCode, httpErr.ErrorCode
}

func Test_LineageService_SyncBranch(t *testing.T) {
	Convey("Test lineageService SyncBranch on a real git store\n", t, func() {
		svc := &lineageService{gs: realGS}
		ctx := context.Background()
		id := newLineageID()
		const branch = "project/p1"

		_, err := realGS.InitLineage(ctx, id, definitions("A part.", "A supplier."), "init", author)
		So(err, ShouldBeNil)
		_, err = svc.CreateBranch(ctx, id, branch, "")
		So(err, ShouldBeNil)

		Convey("A branch that already holds main is left as it is\n", func() {
			head, err := realGS.CommitFiles(ctx, id, branch, definitions("A part, edited.", "A supplier."), "edit", author)
			So(err, ShouldBeNil)

			result, err := svc.SyncBranch(ctx, id, branch, author)

			So(err, ShouldBeNil)
			So(result, ShouldResemble, &interfaces.SyncBranchResult{Result: interfaces.SYNC_RESULT_UP_TO_DATE, Commit: head})
		})

		Convey("A branch without commits of its own fast-forwards to main\n", func() {
			mainHead, err := realGS.CommitFiles(ctx, id, "main", definitions("A part.", "A supplier, edited live."), "drift", author)
			So(err, ShouldBeNil)

			result, err := svc.SyncBranch(ctx, id, branch, author)

			So(err, ShouldBeNil)
			So(result, ShouldResemble, &interfaces.SyncBranchResult{Result: interfaces.SYNC_RESULT_FAST_FORWARD, Commit: mainHead})
			head, _ := realGS.ResolveRef(ctx, id, "refs/heads/"+branch)
			So(head, ShouldEqual, mainHead)
		})

		Convey("Diverged histories merge into a commit with the branch and main as parents\n", func() {
			branchHead, err := realGS.CommitFiles(ctx, id, branch, definitions("A part, edited on the branch.", "A supplier."), "branch edit", author)
			So(err, ShouldBeNil)
			mainHead, err := realGS.CommitFiles(ctx, id, "main", definitions("A part.", "A supplier, edited live."), "drift", author)
			So(err, ShouldBeNil)

			result, err := svc.SyncBranch(ctx, id, branch, author)

			So(err, ShouldBeNil)
			So(result.Result, ShouldEqual, interfaces.SYNC_RESULT_MERGED)
			So(result.Summary, ShouldNotBeNil)
			So(result.Summary.Conflicted, ShouldEqual, 0)

			repo, err := git.PlainOpen(filepath.Join(gitRoot, id+".git"))
			So(err, ShouldBeNil)
			commit, err := repo.CommitObject(plumbing.NewHash(result.Commit))
			So(err, ShouldBeNil)
			So(commit.ParentHashes, ShouldResemble, []plumbing.Hash{plumbing.NewHash(branchHead), plumbing.NewHash(mainHead)})
			So(commit.Author.Name, ShouldEqual, author.Name)

			files, err := realGS.ReadTree(ctx, id, "refs/heads/"+branch)
			So(err, ShouldBeNil)
			So(string(files["object_types/part.bkn"]), ShouldContainSubstring, "A part, edited on the branch.")
			So(string(files["object_types/supplier.bkn"]), ShouldContainSubstring, "A supplier, edited live.")

			Convey("and a second sync has nothing left to do\n", func() {
				again, err := svc.SyncBranch(ctx, id, branch, author)
				So(err, ShouldBeNil)
				So(again.Result, ShouldEqual, interfaces.SYNC_RESULT_UP_TO_DATE)
				So(again.Commit, ShouldEqual, result.Commit)
			})
		})

		Convey("A branch kept in BKN 2.x syncs with a main now kept in BKN 3.0\n", func() {
			_, err := realGS.CommitFiles(ctx, id, branch, definitions("A part, edited on the branch.", "A supplier."), "branch edit", author)
			So(err, ShouldBeNil)
			main3 := definitions("A part.", "A supplier, edited live.")
			main3["network.bkn"] = []byte(strings.Replace(networkText, "name: Supply chain\n", "name: Supply chain\nbkn_spec: \"3.0\"\n", 1))
			_, err = realGS.CommitFiles(ctx, id, "main", main3, "drift as 3.0", author)
			So(err, ShouldBeNil)

			result, err := svc.SyncBranch(ctx, id, branch, author)

			So(err, ShouldBeNil)
			So(result.Result, ShouldEqual, interfaces.SYNC_RESULT_MERGED)
			files, err := realGS.ReadTree(ctx, id, "refs/heads/"+branch)
			So(err, ShouldBeNil)
			So(string(files["network.bkn"]), ShouldContainSubstring, `bkn_spec: "3.0"`)
			So(string(files["object_types/part.bkn"]), ShouldContainSubstring, "A part, edited on the branch.")
		})

		Convey("A conflict replies 409 with the conflicts and commits nothing\n", func() {
			branchHead, err := realGS.CommitFiles(ctx, id, branch, definitions("Branch wording.", "A supplier."), "branch edit", author)
			So(err, ShouldBeNil)
			_, err = realGS.CommitFiles(ctx, id, "main", definitions("Live wording.", "A supplier."), "drift", author)
			So(err, ShouldBeNil)

			_, err = svc.SyncBranch(ctx, id, branch, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Lineage_MergeConflict)
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			details := httpErr.ErrorDetails.(map[string]any)
			conflicts := details["conflicts"].([]interfaces.MergeConflict)
			So(conflicts, ShouldNotBeEmpty)
			So(conflicts[0].ID, ShouldEqual, "part")
			head, _ := realGS.ResolveRef(ctx, id, "refs/heads/"+branch)
			So(head, ShouldEqual, branchHead)
		})

		Convey("main, bad names, unknown branches and lineages are refused\n", func() {
			_, err := svc.SyncBranch(ctx, id, "main", author)
			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusForbidden)
			So(code, ShouldEqual, perrors.BknProject_Lineage_MainBranchReadOnly)

			_, err = svc.SyncBranch(ctx, id, "a..b", author)
			status, _ = httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)

			_, err = svc.SyncBranch(ctx, id, "project/none", author)
			_, code = httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_BranchNotFound)

			_, err = svc.SyncBranch(ctx, "missing", branch, author)
			_, code = httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_NotFound)
		})
	})
}

func Test_LineageService_CreateLineage(t *testing.T) {
	Convey("Test lineageService CreateLineage\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		bba := pmock.NewMockBknBackendAccess(mockCtrl)
		svc := &lineageService{gs: realGS, bba: bba}
		ctx := context.Background()
		id := newLineageID()
		live := definitions("A part.", "A supplier.")

		Convey("The live definitions become the first commit on main\n", func() {
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)

			result, err := svc.CreateLineage(ctx, token, id, author)

			So(err, ShouldBeNil)
			So(result.LineageID, ShouldEqual, id)
			head, _ := realGS.ResolveRef(ctx, id, "main")
			So(result.Commit, ShouldEqual, head)
			files, _ := realGS.ReadTree(ctx, id, "main")
			So(files, ShouldResemble, live)

			Convey("and a second create replies 409\n", func() {
				bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)

				_, err := svc.CreateLineage(ctx, token, id, author)

				status, code := httpCode(err)
				So(status, ShouldEqual, http.StatusConflict)
				So(code, ShouldEqual, perrors.BknProject_Lineage_AlreadyExists)
			})
		})

		Convey("A refusal from bkn-backend keeps its status\n", func() {
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusForbidden})

			_, err := svc.CreateLineage(ctx, token, id, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusForbidden)
			So(code, ShouldEqual, perrors.BknProject_Lineage_BknBackendRequestFailed)
		})

		Convey("A network bkn-backend cannot export as BKN 3.0 says what to fix, and leaves no lineage\n", func() {
			found := map[string]any{"misplaced_parameters": []any{map[string]any{"action_type_id": "restock"}}}
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(nil, &interfaces.BknBackendError{
				HTTPCode: http.StatusBadRequest,
				BaseError: httperrors.BaseError{
					ErrorCode:    "BknBackend.KnowledgeNetwork.ExportNeedsNetworkCheck",
					ErrorDetails: found,
				},
			})

			_, err := svc.CreateLineage(ctx, token, id, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Lineage_NetworkNeedsCheck)
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.ErrorDetails, ShouldResemble, found)
			_, refErr := realGS.ResolveRef(ctx, id, "main")
			So(refErr, ShouldNotBeNil)
		})

		Convey("A network whose definitions do not read back as BKN 3.0 names the one to fix, and leaves no lineage\n", func() {
			const unreadable = `load object_types/part.bkn: data property name is empty`
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(nil, &interfaces.BknBackendError{
				HTTPCode: http.StatusBadRequest,
				BaseError: httperrors.BaseError{
					ErrorCode:    "BknBackend.KnowledgeNetwork.ExportNotReadableAsV3",
					ErrorDetails: unreadable,
				},
			})

			_, err := svc.CreateLineage(ctx, token, id, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Lineage_NetworkNotReadableAsV3)
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.ErrorDetails, ShouldEqual, unreadable)
			_, refErr := realGS.ResolveRef(ctx, id, "main")
			So(refErr, ShouldNotBeNil)
		})

		Convey("A bkn-backend server error is a bad gateway\n", func() {
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusInternalServerError})

			_, err := svc.CreateLineage(ctx, token, id, author)

			status, _ := httpCode(err)
			So(status, ShouldEqual, http.StatusBadGateway)
		})

		Convey("An empty kn_id is refused\n", func() {
			_, err := svc.CreateLineage(ctx, token, "", author)

			_, code := httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_InvalidKNID)
		})
	})
}

func Test_LineageService_CreateBranch(t *testing.T) {
	Convey("Test lineageService CreateBranch\n", t, func() {
		svc := &lineageService{gs: realGS}
		ctx := context.Background()
		id := newLineageID()
		first, err := realGS.InitLineage(ctx, id, definitions("A part.", "A supplier."), "init", author)
		So(err, ShouldBeNil)

		Convey("A branch starts at main by default\n", func() {
			result, err := svc.CreateBranch(ctx, id, "project/p1", "")

			So(err, ShouldBeNil)
			So(result, ShouldResemble, &interfaces.CreateBranchResult{Name: "project/p1", Commit: first})

			_, err = svc.CreateBranch(ctx, id, "project/p1", "main")
			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Lineage_BranchAlreadyExists)
		})

		Convey("Invalid names are refused\n", func() {
			for _, name := range []string{"", "main", "a..b", "a b", "x/"} {
				_, err := svc.CreateBranch(ctx, id, name, "")
				status, code := httpCode(err)
				So(status, ShouldEqual, http.StatusBadRequest)
				So(code, ShouldEqual, perrors.BknProject_Lineage_InvalidBranchName)
			}
		})

		Convey("An unknown start or lineage replies 404\n", func() {
			_, err := svc.CreateBranch(ctx, id, "project/p1", "no-such-ref")
			_, code := httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_BranchNotFound)

			_, err = svc.CreateBranch(ctx, "missing", "project/p1", "")
			_, code = httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_NotFound)
		})
	})
}

func Test_LineageService_CommitDefinitions(t *testing.T) {
	Convey("Test lineageService CommitDefinitions\n", t, func() {
		svc := &lineageService{gs: realGS}
		ctx := context.Background()
		id := newLineageID()
		base := definitions("A part.", "A supplier.")
		_, err := realGS.InitLineage(ctx, id, base, "init", author)
		So(err, ShouldBeNil)
		_, err = svc.CreateBranch(ctx, id, "project/p1", "")
		So(err, ShouldBeNil)

		Convey("The upload replaces the branch definitions and lists what changed\n", func() {
			next := definitions("A part, edited.", "A supplier.")
			delete(next, "object_types/supplier.bkn")
			next["object_types/plant.bkn"] = objectType("plant", "Plant", "A plant.")

			result, err := svc.CommitDefinitions(ctx, id, "project/p1", next, "", author)

			So(err, ShouldBeNil)
			So(result.Changed, ShouldResemble, interfaces.PathDiff{
				Added:   []string{"object_types/plant.bkn"},
				Changed: []string{"object_types/part.bkn"},
				Removed: []string{"object_types/supplier.bkn"},
			})
			head, _ := realGS.ResolveRef(ctx, id, "refs/heads/project/p1")
			So(result.Commit, ShouldEqual, head)

			Convey("and the same upload again makes no commit\n", func() {
				again, err := svc.CommitDefinitions(ctx, id, "project/p1", next, "again", author)
				So(err, ShouldBeNil)
				So(again.Commit, ShouldEqual, result.Commit)
				So(again.Changed.Empty(), ShouldBeTrue)
			})
		})

		Convey("main, an empty upload and an unknown branch are refused\n", func() {
			_, err := svc.CommitDefinitions(ctx, id, "main", base, "", author)
			mainStatus, _ := httpCode(err)
			So(mainStatus, ShouldEqual, http.StatusForbidden)

			_, err = svc.CommitDefinitions(ctx, id, "project/p1", map[string][]byte{}, "", author)
			status, code := httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_InvalidDefinitions)

			// A set the BKN loader cannot read would only fail later, in sync or publish.
			_, err = svc.CommitDefinitions(ctx, id, "project/p1",
				map[string][]byte{"object_types/part.bkn": []byte("---\ntype: object_type\nid: part\n---\n")}, "", author)
			status, code = httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Lineage_InvalidDefinitions)

			// The loader reads it, but git cannot store "notes" as a file and a directory.
			clash := definitions("A part.", "A supplier.")
			clash["notes"] = []byte("a file")
			clash["notes/a.md"] = []byte("a file below it")
			_, err = svc.CommitDefinitions(ctx, id, "project/p1", clash, "", author)
			status, code = httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Lineage_InvalidDefinitions)

			_, err = svc.CommitDefinitions(ctx, id, "project/none", base, "", author)
			_, code = httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_BranchNotFound)
		})
	})
}

func Test_LineageService_AddDefinitions(t *testing.T) {
	Convey("Test lineageService AddDefinitions\n", t, func() {
		svc := &lineageService{gs: realGS}
		ctx := context.Background()
		id := newLineageID()
		const branch = "project/p1"
		base := definitions("A part.", "A supplier.")
		base["network.bkn"] = []byte(strings.Replace(networkText, "name: Supply chain\n", "name: Supply chain\nbkn_spec: \"3.0\"\n", 1))
		_, err := realGS.InitLineage(ctx, id, base, "init", author)
		So(err, ShouldBeNil)
		_, err = svc.CreateBranch(ctx, id, branch, "")
		So(err, ShouldBeNil)

		Convey("The files join what the branch holds, which stays as it is\n", func() {
			plant := objectType("plant", "Plant", "A plant.")

			result, err := svc.AddDefinitions(ctx, id, branch, map[string][]byte{"object_types/plant.bkn": plant}, nil, "confirm", author)

			So(err, ShouldBeNil)
			So(result.Changed, ShouldResemble, interfaces.PathDiff{Added: []string{"object_types/plant.bkn"}, Changed: []string{}, Removed: []string{}})
			files, err := realGS.ReadTree(ctx, id, "refs/heads/"+branch)
			So(err, ShouldBeNil)
			So(files["object_types/plant.bkn"], ShouldResemble, plant)
			So(files["object_types/part.bkn"], ShouldResemble, base["object_types/part.bkn"])
			So(files["network.bkn"], ShouldResemble, base["network.bkn"])
		})

		Convey("What the same source wrote before is replaced, and removed once no longer written\n", func() {
			first := map[string][]byte{
				"object_types/plant.bkn": objectType("plant", "Plant", "A plant."),
				"object_types/depot.bkn": objectType("depot", "Depot", "A depot."),
			}
			_, err := svc.AddDefinitions(ctx, id, branch, first, nil, "confirm", author)
			So(err, ShouldBeNil)
			again := map[string][]byte{"object_types/plant.bkn": objectType("plant", "Plant", "A plant, renamed.")}

			result, err := svc.AddDefinitions(ctx, id, branch, again,
				[]string{"object_types/plant.bkn", "object_types/depot.bkn"}, "confirm again", author)

			So(err, ShouldBeNil)
			So(result.Changed, ShouldResemble, interfaces.PathDiff{
				Added: []string{}, Changed: []string{"object_types/plant.bkn"}, Removed: []string{"object_types/depot.bkn"},
			})
			files, _ := realGS.ReadTree(ctx, id, "refs/heads/"+branch)
			So(files["object_types/part.bkn"], ShouldResemble, base["object_types/part.bkn"])
			_, kept := files["object_types/depot.bkn"]
			So(kept, ShouldBeFalse)
		})

		Convey("A path the branch holds is refused and listed, and nothing is committed\n", func() {
			before, _ := realGS.ResolveRef(ctx, id, "refs/heads/"+branch)

			_, err := svc.AddDefinitions(ctx, id, branch, map[string][]byte{
				"object_types/part.bkn":  objectType("part", "Part", "Another part."),
				"object_types/plant.bkn": objectType("plant", "Plant", "A plant."),
			}, nil, "", author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Lineage_DefinitionsExist)
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.ErrorDetails, ShouldResemble, []string{"object_types/part.bkn"})
			after, _ := realGS.ResolveRef(ctx, id, "refs/heads/"+branch)
			So(after, ShouldEqual, before)
		})

		Convey("Files the BKN loader cannot read with the branch are refused\n", func() {
			_, err := svc.AddDefinitions(ctx, id, branch,
				map[string][]byte{"object_types/plant.bkn": []byte("---\ntype: object_type\nid: plant\n---\n")}, nil, "", author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Lineage_InvalidDefinitions)
		})

		Convey("A branch still kept in BKN 2.x is refused until it syncs\n", func() {
			id2 := newLineageID()
			_, err := realGS.InitLineage(ctx, id2, definitions("A part.", "A supplier."), "init", author)
			So(err, ShouldBeNil)
			_, err = svc.CreateBranch(ctx, id2, branch, "")
			So(err, ShouldBeNil)

			_, err = svc.AddDefinitions(ctx, id2, branch,
				map[string][]byte{"object_types/plant.bkn": objectType("plant", "Plant", "A plant.")}, nil, "", author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Lineage_BranchNotV3)
		})

		Convey("main, nothing to add and an unknown branch are refused\n", func() {
			_, err := svc.AddDefinitions(ctx, id, "main", base, nil, "", author)
			status, _ := httpCode(err)
			So(status, ShouldEqual, http.StatusForbidden)

			_, err = svc.AddDefinitions(ctx, id, branch, map[string][]byte{}, nil, "", author)
			_, code := httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_InvalidDefinitions)

			_, err = svc.AddDefinitions(ctx, id, "project/none",
				map[string][]byte{"object_types/plant.bkn": objectType("plant", "Plant", "A plant.")}, nil, "", author)
			_, code = httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_BranchNotFound)
		})
	})
}

func Test_LineageService_CheckDrift(t *testing.T) {
	Convey("Test lineageService CheckDrift\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ds := pmock.NewMockDriftService(mockCtrl)
		ra := pmock.NewMockReleaseAccess(mockCtrl)
		svc := &lineageService{ds: ds, ra: ra}
		ctx := context.Background()
		// Before its first release a lineage's live network is the network it was started from.
		ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "orders", Limit: 1}).
			Return([]*interfaces.Release{}, nil).AnyTimes()
		drifted := &interfaces.DriftReport{LineageID: "orders", KNID: "orders",
			PathDiff: interfaces.PathDiff{Added: []string{}, Changed: []string{"network.bkn"}, Removed: []string{}}}

		Convey("A check alone reports the drift and commits nothing\n", func() {
			ds.EXPECT().DriftCheck(gomock.Any(), token, "orders", "orders").Return(drifted, nil)

			result, err := svc.CheckDrift(ctx, token, "orders", "orders", false, author)

			So(err, ShouldBeNil)
			So(result.DriftReport, ShouldEqual, drifted)
			So(result.Commit, ShouldBeEmpty)
		})

		Convey("With commit the live set is committed to main\n", func() {
			ds.EXPECT().DriftCheck(gomock.Any(), token, "orders", "orders").Return(drifted, nil)
			ds.EXPECT().CommitLiveToMain(gomock.Any(), drifted, author).Return("c2", nil)

			result, err := svc.CheckDrift(ctx, token, "orders", "orders", true, author)

			So(err, ShouldBeNil)
			So(result.Commit, ShouldEqual, "c2")
		})

		Convey("With commit and no drift nothing is committed\n", func() {
			clean := &interfaces.DriftReport{LineageID: "orders", KNID: "orders"}
			ds.EXPECT().DriftCheck(gomock.Any(), token, "orders", "orders").Return(clean, nil)

			result, err := svc.CheckDrift(ctx, token, "orders", "orders", true, author)

			So(err, ShouldBeNil)
			So(result.Commit, ShouldBeEmpty)
		})

		Convey("A missing network or lineage replies 404\n", func() {
			ds.EXPECT().DriftCheck(gomock.Any(), token, "orders", "orders").
				Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusNotFound})
			_, err := svc.CheckDrift(ctx, token, "orders", "orders", false, author)
			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusNotFound)
			So(code, ShouldEqual, perrors.BknProject_Lineage_BknBackendRequestFailed)

			ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "missing", Limit: 1}).
				Return([]*interfaces.Release{}, nil)
			ds.EXPECT().DriftCheck(gomock.Any(), token, "missing", "missing").
				Return(nil, fmt.Errorf("read main of lineage missing: %w", git.ErrRepositoryNotExists))
			_, err = svc.CheckDrift(ctx, token, "missing", "missing", false, author)
			_, code = httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_NotFound)
		})

		Convey("A network that is not the lineage's live network is refused before anything is read\n", func() {
			_, err := svc.CheckDrift(ctx, token, "orders", "x", true, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Lineage_NotLiveNetwork)
		})

		Convey("After a release only the released network is live, not the one it replaced\n", func() {
			released := pmock.NewMockReleaseAccess(mockCtrl)
			svc := &lineageService{ds: ds, ra: released}
			released.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "orders", Limit: 1}).
				Return([]*interfaces.Release{{LineageID: "orders", NetworkID: "orders_v1_1_0", Version: "1.1.0"}}, nil).Times(2)

			_, err := svc.CheckDrift(ctx, token, "orders", "orders", true, author)
			_, code := httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_NotLiveNetwork)

			ds.EXPECT().DriftCheck(gomock.Any(), token, "orders", "orders_v1_1_0").Return(drifted, nil)
			_, err = svc.CheckDrift(ctx, token, "orders", "orders_v1_1_0", false, author)
			So(err, ShouldBeNil)
		})

		Convey("An empty kn_id is refused\n", func() {
			_, err := svc.CheckDrift(ctx, token, "orders", "", false, author)

			_, code := httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_InvalidKNID)
		})
	})
}
