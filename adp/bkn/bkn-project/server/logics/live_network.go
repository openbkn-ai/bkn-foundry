// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// LiveNetwork returns the network of the lineage: the lineage id, as a versioned network keeps its id
// across releases.
func LiveNetwork(ctx context.Context, ra interfaces.ReleaseAccess, lineageID string) (string, error) {
	network, _, err := LiveNetworkOf(ctx, ra, lineageID)
	return network, err
}

// LiveNetworkOf is LiveNetwork, and whether the lineage was ever released: before that, what the
// project models is only on its branch, not live anywhere.
func LiveNetworkOf(ctx context.Context, ra interfaces.ReleaseAccess, lineageID string) (string, bool, error) {
	latest, err := ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: lineageID, State: interfaces.RELEASE_STATE_PUBLISHED, Limit: 1})
	if err != nil {
		return "", false, err
	}
	return lineageID, len(latest) > 0, nil
}
