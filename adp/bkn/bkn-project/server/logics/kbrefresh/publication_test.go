// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

type refreshReleases struct {
	interfaces.ReleaseAccess
	sw      *interfaces.Switch
	release *interfaces.Release
	fail    bool
	lookups []string
}

func (r *refreshReleases) GetLatestSwitch(context.Context, string) (*interfaces.Switch, bool, error) {
	if r.fail {
		return nil, false, errors.New("unavailable")
	}
	return r.sw, r.sw != nil, nil
}
func (r *refreshReleases) GetReleaseByNetworkID(_ context.Context, network string) (*interfaces.Release, bool, error) {
	r.lookups = append(r.lookups, network)
	if r.release == nil {
		return nil, false, nil
	}
	return r.release, true, nil
}

type refreshPublications struct {
	interfaces.KBPublicationAccess
	value   *interfaces.KBPublication
	lookups []string
}

func (p *refreshPublications) Get(_ context.Context, network string) (*interfaces.KBPublication, error) {
	p.lookups = append(p.lookups, network)
	return p.value, nil
}
func TestRefreshUsesExplicitConsumerSwitchInsteadOfNewestRelease(t *testing.T) {
	owner := interfaces.AccountInfo{ID: "owner", Type: "user"}
	for _, scenario := range []string{"base", "switched", "rollback", "wrong_lineage", "missing_release", "not_active", "wrong_publisher", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			target := "base"
			if scenario != "base" {
				target = "base_v1"
			}
			if scenario == "rollback" {
				target = "base_v0"
			}
			releases := &refreshReleases{release: &interfaces.Release{NetworkID: target, LineageID: "base", ProjectID: "archived-project", Publisher: owner}}
			if scenario != "base" {
				releases.sw = &interfaces.Switch{LineageID: "base", ToNetworkID: target}
			}
			publications := &refreshPublications{value: &interfaces.KBPublication{NetworkID: target, LineageID: "base", ProjectID: "archived-project", Publisher: owner, State: "active", GenerationID: "generation", FullBuildID: "full", Revision: 3}}
			switch scenario {
			case "wrong_lineage":
				releases.release.LineageID = "other"
			case "missing_release":
				releases.release = nil
			case "not_active":
				publications.value.State = "mounted"
			case "wrong_publisher":
				publications.value.Publisher.ID = "different"
			case "unavailable":
				releases.fail = true
			}
			record, err := Resolve(context.Background(), "base", releases, publications)
			valid := scenario == "base" || scenario == "switched" || scenario == "rollback"
			if (err == nil) != valid {
				t.Fatalf("wrong publication accepted %+v: %v", record, err)
			}
			if valid && (record.NetworkID != target || len(releases.lookups) != 1 || releases.lookups[0] != target || len(publications.lookups) != 1 || publications.lookups[0] != target) {
				t.Fatal("newest publication incorrectly became live")
			}
		})
	}
}
