// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// LiveNetwork returns the network of the lineage's latest release, or the lineage id, which is the
// network the lineage was started from, before the first release. Every read of what is live goes
// through it, so a lineage that serves one network across releases changes only this.
func LiveNetwork(ctx context.Context, ra interfaces.ReleaseAccess, lineageID string) (string, error) {
	network, _, err := LiveNetworkOf(ctx, ra, lineageID)
	return network, err
}

// LiveNetworkOf is LiveNetwork, and whether the lineage was ever released: before that, what the
// project models is only on its branch, not live anywhere.
func LiveNetworkOf(ctx context.Context, ra interfaces.ReleaseAccess, lineageID string) (string, bool, error) {
	latest, err := ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: lineageID, Limit: 1})
	if err != nil {
		return "", false, err
	}
	if len(latest) == 0 {
		return lineageID, false, nil
	}
	return latest[0].NetworkID, true, nil
}
