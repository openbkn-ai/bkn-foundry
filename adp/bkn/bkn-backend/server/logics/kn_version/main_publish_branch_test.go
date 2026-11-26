// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_version

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// stateAccess serves the version state and versions the prechecks read; anything else panics.
type stateAccess struct {
	interfaces.KNVersionAccess
	state    *interfaces.KNVersionState
	versions map[int]*interfaces.KNVersion
}

func (a stateAccess) GetState(context.Context, *sql.Tx, string, bool) (*interfaces.KNVersionState, error) {
	return a.state, nil
}

func (a stateAccess) GetVersion(_ context.Context, _ *sql.Tx, _ string, k int, _ bool) (*interfaces.KNVersion, error) {
	return a.versions[k], nil
}

var errStopAtMain = errors.New("stop at the main publisher")

// branchPublisher records the branch each main publication copies from, then stops it.
type branchPublisher struct {
	interfaces.KNMainPublisher
	sources *[]string
}

func (p branchPublisher) CheckReferences(context.Context, string, string) ([]*interfaces.KNVersionProblem, error) {
	return nil, nil
}

func (p branchPublisher) PublishKNMain(_ context.Context, _, srcBranch string, _ func(context.Context, *sql.Tx) error) error {
	*p.sources = append(*p.sources, srcBranch)
	return errStopAtMain
}

// Publishing the draft and switching to a version both go through the main publisher, which plans
// the proxy grants of the new main from the branch named here (#3444).
func TestPublishAndActivateReplaceMainFromTheirBranch(t *testing.T) {
	var sources []string
	svc := &service{
		va: stateAccess{
			state: &interfaces.KNVersionState{KNID: "kn-1", Mode: interfaces.KNVersioningEnabled,
				RunningVersion: 3, PreviousVersion: 2, NextVersion: 4, DraftRevision: 5, DraftModified: true,
				DraftBaseVersion: 3, DraftBasedOnRunning: 3},
			versions: map[int]*interfaces.KNVersion{2: {KNID: "kn-1", Version: 2, Status: interfaces.KNVersionHistory}},
		},
		ps:          permitAll{},
		pub:         branchPublisher{sources: &sources},
		fullHistory: func() bool { return true },
		now:         func() int64 { return 1 },
	}
	ctx := context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "u1", Type: "user"})
	running, revision := 3, int64(5)

	if _, err := svc.Publish(ctx, "kn-1", &interfaces.KNPublishRequest{
		ExpectedRunningVersion: &running, ExpectedDraftRevision: &revision,
	}); err == nil {
		t.Fatal("Publish did not reach the main publisher")
	}
	if _, err := svc.Activate(ctx, "kn-1", 2, &interfaces.KNActivateRequest{ExpectedRunningVersion: &running}); err == nil {
		t.Fatal("Activate did not reach the main publisher")
	}
	want := []string{interfaces.DRAFT_BRANCH, interfaces.VersionBranch(2)}
	if len(sources) != len(want) || sources[0] != want[0] || sources[1] != want[1] {
		t.Fatalf("main publications copied from %v, want %v", sources, want)
	}
}
