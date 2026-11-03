// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

// VerifyCompleted reuses the exact reviewed linking/fusion preparation without
// claiming a writer or changing rows. Its caller supplies current publisher,
// source and model authorization through guard; no permission is inferred from
// an earlier successful task or a stored receipt.
func (s *SystemLinkService) VerifyCompleted(ctx context.Context, record *interfaces.KBBuildRecord, draft *interfaces.Draft, guard func(context.Context) error) (*SystemLinkPreview, error) {
	if s.Datasets == nil || s.Vega == nil || record == nil || record.Build == nil || draft == nil || guard == nil {
		return nil, ErrInvalidBuild
	}
	build := record.Build
	if build.Mode != "full" || build.State != "succeeded" || !record.Identity.Cleaned || draft.Items.Confirmation == nil || draft.UpdateTime != build.DraftUpdateTime || draft.Items.Confirmation.UpdateTime != draft.UpdateTime || draft.Items.Confirmation.Commit != build.DefinitionCommit {
		return nil, ErrBuildChanged
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil || fingerprint != build.ModelFingerprint {
		return nil, ErrBuildChanged
	}
	for _, file := range build.Files {
		if file.State != "succeeded" {
			return nil, ErrBuildChanged
		}
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	prepared, err := s.prepare(ctx, record, draft, guard)
	if err != nil {
		return nil, err
	}
	required := len(prepared.Preview.Relations) > 0 || len(prepared.Preview.Fusion) > 0 || len(prepared.Preview.Facts) > 0
	receipt := build.Linking
	if required && (receipt == nil || receipt.State != "succeeded" || !receipt.FusionApplied || receipt.Signature != prepared.Preview.Signature) {
		return nil, ErrBuildChanged
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return &prepared.Preview, nil
}
