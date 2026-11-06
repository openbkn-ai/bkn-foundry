// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"

	"github.com/go-git/go-git/v5"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// publicationProbe is an edition that publishes a part of each release, recording what it is
// asked to do.
type publicationProbe struct {
	prepare  func(projecthooks.ReleaseTarget) (projecthooks.Publication, error)
	mount    func(network string) error
	activate func() error
}

func (p *publicationProbe) Review(context.Context, interfaces.GitStore, string, *interfaces.Draft, projecthooks.ReleaseTarget) (interfaces.ReleaseCheck, func(string) string, bool) {
	return interfaces.ReleaseCheck{}, nil, false
}
func (p *publicationProbe) Prepare(_ context.Context, _ interfaces.GitStore, _ string, target projecthooks.ReleaseTarget) (projecthooks.Publication, error) {
	return p.prepare(target)
}

type probedPublication struct {
	probe            *publicationProbe
	project, network string
}

func (p probedPublication) ProjectID() string                   { return p.project }
func (p probedPublication) Mount(context.Context, string) error { return p.probe.mount(p.network) }
func (p probedPublication) Activate(context.Context) error      { return p.probe.activate() }

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
			var probe *publicationProbe
			probe = &publicationProbe{
				prepare: func(target projecthooks.ReleaseTarget) (projecthooks.Publication, error) {
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
						return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Release_SourcePermission)
					}
					return probedPublication{probe: probe, project: "p1", network: network}, nil
				},
				mount: func(mounted string) error {
					events = append(events, "mount")
					if mounted != network {
						t.Fatal("wrong mounted network")
					}
					if scenario == "mount_uncertain" {
						return errors.New("mount not acknowledged")
					}
					return nil
				},
				activate: func() error {
					events = append(events, "activate")
					if scenario == "activation_uncertain" {
						return errors.New("activation acknowledgement lost")
					}
					return nil
				},
			}
			svc := &lineageService{gs: realGS, bba: bba, ra: releases}
			useHooks(t, projecthooks.Hooks{Publications: probe})
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
	t.Cleanup(projecthooks.SetForTest(hooks))
}
