// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// nameOwners sets the owner's display name of each project in a reply. Only replies are named:
// accounts are compared and stored by id and type alone.
func (r *restHandler) nameOwners(ctx context.Context, entries ...*interfaces.ProjectEntry) {
	accounts := []interfaces.AccountInfo{}
	for _, e := range entries {
		if e != nil && e.Project != nil {
			accounts = append(accounts, e.Owner)
		}
	}
	if len(accounts) == 0 || r.ana == nil {
		return
	}
	names := r.ana.Names(ctx, accounts)
	for _, e := range entries {
		if e != nil && e.Project != nil {
			e.OwnerName = names[e.Owner]
		}
	}
}

// namePublishers sets the publisher's display name of each version and of the prepared release.
func (r *restHandler) namePublishers(ctx context.Context, versions *interfaces.ProjectVersions) {
	if versions == nil {
		return
	}
	accounts := []interfaces.AccountInfo{}
	for _, v := range versions.Entries {
		accounts = append(accounts, v.Publisher)
	}
	if versions.Prepared != nil {
		accounts = append(accounts, versions.Prepared.Publisher)
	}
	if len(accounts) == 0 || r.ana == nil {
		return
	}
	names := r.ana.Names(ctx, accounts)
	for i := range versions.Entries {
		versions.Entries[i].PublisherName = names[versions.Entries[i].Publisher]
	}
	if versions.Prepared != nil {
		prepared := *versions.Prepared
		prepared.PublisherName = names[prepared.Publisher]
		versions.Prepared = &prepared
	}
}
