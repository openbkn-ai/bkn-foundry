// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"strings"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"

	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func knowledgeNetworkInvalidDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.KnowledgeNetwork.InvalidParameter.Detail."+name, templateData)
}

// validateKnowledgeNetwork validates required knowledge network fields.
func ValidateKN(ctx context.Context, kn *interfaces.KN) error {
	// Validate the ID.
	err := validateID(ctx, kn.KNID)
	if err != nil {
		return err
	}

	// Trim and validate the model name.
	kn.KNName = strings.TrimSpace(kn.KNName)
	err = validateObjectName(ctx, kn.KNName, interfaces.MODULE_TYPE_KN)
	if err != nil {
		return err
	}

	// Validate tags when provided.
	err = ValidateTags(ctx, kn.Tags)
	if err != nil {
		return err
	}

	// Trim tags and remove duplicates.
	kn.Tags = libCommon.TagSliceTransform(kn.Tags)

	// Validate that the branch is not empty.
	if kn.Branch == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_NullParameter_Branch).
			WithErrorDetails(knowledgeNetworkInvalidDetail(ctx, "BranchRequired", nil))
	}

	return nil
}

// validatePathQuery validates path query parameters.
func ValidateRelationTypePathsQuery(ctx context.Context, query *interfaces.RelationTypePathsBaseOnSource) error {
	// The source object type is required.
	if query.SourceObjecTypeId == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_NullParameter_SourceObjectTypeId)
	}

	// The direction is required.
	if query.Direction == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_NullParameter_Direction)
	}

	// Validate the direction.
	if !interfaces.DIRECTION_MAP[query.Direction] {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter_Direction).
			WithErrorDetails(knowledgeNetworkInvalidDetail(ctx, "DirectionUnsupported", map[string]any{"direction": query.Direction}))
	}

	// The path length must not exceed three.
	if query.PathLength > 3 || query.PathLength < 1 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter_PathLength).
			WithErrorDetails(knowledgeNetworkInvalidDetail(ctx, "PathLengthInvalid", map[string]any{"pathLength": query.PathLength}))
	}

	return nil
}
