// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
	"errors"
	"github.com/go-git/go-git/v5"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbpublication"
	"go.uber.org/mock/gomock"
)

type publicationProbe struct {
	prepare  func(kbpublication.Target) (*interfaces.KBPublication, error)
	mount    func(*interfaces.KBPublication) error
	activate func(*interfaces.KBPublication) error
}

func (p *publicationProbe) Prepare(_ context.Context, _ string, target kbpublication.Target) (*interfaces.KBPublication, error) {
	return p.prepare(target)
}
func (p *publicationProbe) Mount(_ context.Context, _ string, record *interfaces.KBPublication) error {
	return p.mount(record)
}
func (p *publicationProbe) Activate(_ context.Context, record *interfaces.KBPublication) error {
	return p.activate(record)
}
func TestKnowledgePublicationFencesDirectReleaseAndMountsBeforeRecording(t *testing.T) {
	for _, scenario := range []string{"publish", "source_denied", "mount_uncertain", "review_outdated", "activation_uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			bba := pmock.NewMockBknBackendAccess(ctrl)
			releases := pmock.NewMockReleaseAccess(ctrl)
			ctx := context.Background()
			id := newLineageID()
			network := id + "_v1_0_0"
			branch := "project/p1"
			live := definitions("A part.", "A supplier.")
			if _, err := realGS.InitLineage(ctx, id, live, "init", author); err != nil {
				t.Fatal(err)
			}
			if err := realGS.CreateBranch(ctx, id, branch, "main"); err != nil {
				t.Fatal(err)
			}
			if scenario == "activation_uncertain" {
				if _, err := realGS.CommitFiles(ctx, id, branch, definitions("Changed part.", "A supplier."), "edit model", author); err != nil {
					t.Fatal(err)
				}
			}
			successful := scenario == "publish" || scenario == "activation_uncertain"
			events := []string{}
			probe := &publicationProbe{
				prepare: func(target kbpublication.Target) (*interfaces.KBPublication, error) {
					events = append(events, "prepare")
					if target.ProjectID != "p1" || target.LineageID != id || target.NetworkID != network || target.Publisher != publisher {
						t.Fatal("wrong publisher scope")
					}
					repo, err := git.PlainOpen(filepath.Join(gitRoot, id+".git"))
					if err != nil {
						t.Fatal(err)
					}
					if _, err := repo.Reference(plumbing.NewTagReferenceName("v1.0.0"), true); !errors.Is(err, plumbing.ErrReferenceNotFound) {
						t.Fatal("tag created before knowledge preflight", err)
					}
					if scenario == "source_denied" {
						return nil, &kb.SourceError{StatusCode: 403}
					}
					return &interfaces.KBPublication{ProjectID: "p1", NetworkID: network}, nil
				},
				mount: func(record *interfaces.KBPublication) error {
					events = append(events, "mount")
					if record == nil || record.NetworkID != network {
						t.Fatal("wrong mounted network")
					}
					if scenario == "mount_uncertain" {
						return errors.New("mount not acknowledged")
					}
					return nil
				},
				activate: func(*interfaces.KBPublication) error {
					events = append(events, "activate")
					if scenario == "activation_uncertain" {
						return errors.New("activation acknowledgement lost")
					}
					return nil
				},
			}
			svc := &lineageService{gs: realGS, bba: bba, ra: releases}
			useHooks(t, projecthooks.Hooks{Publications: kbpublication.Hooks{Using: func(interfaces.GitStore) kbpublication.Publisher { return probe }}})
			releases.EXPECT().GetReleaseByNetworkID(gomock.Any(), network).Return(nil, false, nil)
			releases.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return([]*interfaces.Release{}, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, network).Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusNotFound})
			if successful || scenario == "mount_uncertain" {
				bba.EXPECT().ImportBKN(gomock.Any(), token, gomock.Any()).DoAndReturn(func(context.Context, string, map[string][]byte) (*interfaces.BKNImportResult, error) {
					events = append(events, "import")
					return &interfaces.BKNImportResult{KNID: network}, nil
				})
			}
			if successful {
				releases.EXPECT().CreateRelease(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, release *interfaces.Release) error {
					events = append(events, "record")
					if release.ProjectID != "p1" {
						t.Fatal("direct release lost actual project identity")
					}
					return nil
				})
				bba.EXPECT().ExportBKN(gomock.Any(), token, network).Return(nil, &interfaces.BknBackendError{HTTPCode: 502})
			}
			// A direct caller may omit the informational project_id; the real branch
			// owns the knowledge publication and the resulting ordinary release row.
			request := interfaces.PublishReleaseRequest{Branch: branch, Version: "1.0.0"}
			if scenario == "review_outdated" {
				request.ExpectedBranchCommit = "previous-reviewed-head"
			}
			_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)
			if (err == nil) != successful {
				t.Fatal("incorrect publication outcome", err)
			}
			if scenario == "source_denied" || scenario == "review_outdated" {
				if _, err := realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0"); !errors.Is(err, plumbing.ErrReferenceNotFound) {
					t.Fatal("rejected publication left a tag", err)
				}
			}

			expected := []string{"prepare"}
			if scenario == "review_outdated" {
				expected = []string{}
			}
			if successful || scenario == "mount_uncertain" {
				expected = append(expected, "import", "mount")
			}
			if successful {
				expected = append(expected, "record", "activate")
			}
			if successful {
				main, err := realGS.ResolveRef(ctx, id, "refs/heads/main")
				if err != nil {
					t.Fatal(err)
				}
				tag, err := realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
				if err != nil || main != tag {
					t.Fatal("recorded release did not advance main", err)
				}
			}
			if !reflect.DeepEqual(events, expected) {
				t.Fatal("unsafe release ordering", events)
			}
		})
	}
}

// useHooks registers the hooks given, for one test.
func useHooks(t *testing.T, hooks projecthooks.Hooks) {
	projecthooks.Register(hooks)
	t.Cleanup(func() { projecthooks.Register(projecthooks.Hooks{}) })
}
