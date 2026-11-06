// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbrefresh

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Hooks is what refreshing the data of a lineage adds to it.
type Hooks struct{ Refreshes interfaces.KBRefreshAccess }

var _ projecthooks.Lineages = Hooks{}

// CheckSwitch refuses a switch while a refresh is applying to the network in use: a timed-out
// first write may already have succeeded, so its target stays until the same application recovers.
func (h Hooks) CheckSwitch(ctx context.Context, lineageID string) error {
	if h.Refreshes == nil {
		return nil
	}
	refresh, err := h.Refreshes.Get(ctx, lineageID)
	if err != nil {
		return err
	}
	if refresh != nil && refresh.Pending != nil && refresh.Pending.ApplicationDigest != "" {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_DataUpdateInProgress)
	}
	return nil
}
