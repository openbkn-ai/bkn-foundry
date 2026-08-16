// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"strings"

	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"

	"bkn-backend/interfaces"
)

// ValidateConceptGroup validates required concept group fields.
func ValidateConceptGroup(ctx context.Context, cg *interfaces.ConceptGroup) error {
	// Validate the ID.
	err := validateID(ctx, cg.CGID)
	if err != nil {
		return err
	}

	// Validate the name.
	// Trim surrounding whitespace from the name.
	cg.CGName = strings.TrimSpace(cg.CGName)
	err = validateObjectName(ctx, cg.CGName, interfaces.MODULE_TYPE_CONCEPT_GROUP)
	if err != nil {
		return err
	}

	// Validate tags when provided.
	err = ValidateTags(ctx, cg.Tags)
	if err != nil {
		return err
	}

	// Trim tags and remove duplicates.
	cg.Tags = libCommon.TagSliceTransform(cg.Tags)

	return nil
}
