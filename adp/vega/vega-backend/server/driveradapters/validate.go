// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

func validateID(ctx context.Context, ID string) error {
	if ID == "" {
		return nil
	}

	// Non-built-in view verification logical view id, which only contains lowercase English letters, numbers, underscores (_), and hyphens (-), and cannot start with an underscore and cannot exceed 40 characters
	re := regexp2.MustCompile(interfaces.RegexPattern_NonBuiltin_ID, regexp2.RE2)
	match, err := re.MatchString(ID)
	if err != nil || !match {
		errDetails := `The ID can contain only lowercase letters, digits, underscores(_) and hyphens(-),
			it must start with a lowercase letter or digit and cannot exceed 40 characters`
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails(errDetails)
	}

	return nil
}

// Name legitimacy verification
func validateName(ctx context.Context, name string) error {
	if name == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Name)
	}

	if utf8.RuneCountInString(name) > interfaces.NAME_MAX_LENGTH {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Name).
			WithErrorDetails(fmt.Sprintf("The length of the name %v exceeds %v", name, interfaces.NAME_MAX_LENGTH))
	}

	return nil
}

// The legitimacy verification of tags
func ValidateTags(ctx context.Context, Tags []string) error {
	if len(Tags) > interfaces.TAGS_MAX_NUMBER {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Tag).
			WithErrorDetails(fmt.Sprintf("The number of tags exceeds %v", interfaces.TAGS_MAX_NUMBER))
	}

	for _, tag := range Tags {
		err := validateTag(ctx, tag)
		if err != nil {
			return err
		}
	}
	return nil
}

// Verification of the legitimacy of data tag names
func validateTag(ctx context.Context, tag string) error {
	// Remove the left and right Spaces of the tag
	tag = strings.Trim(tag, " ")

	if tag == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Tag)
		// .WithErrorDetails("Data tag name is null")
	}

	if utf8.RuneCountInString(tag) > interfaces.TAG_MAX_LENGTH {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Tag).
			WithErrorDetails(fmt.Sprintf("The length of the tag name exceeds %d", interfaces.TAG_MAX_LENGTH))
	}

	if isInvalid := strings.ContainsAny(tag, interfaces.TAG_INVALID_CHARACTER); isInvalid {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Tag).
			WithErrorDetails(fmt.Sprintf("Tag name contains special characters, such as %s", interfaces.TAG_INVALID_CHARACTER))
	}

	return nil
}

// Note Legality verification
func validateDescription(ctx context.Context, description string) error {
	if utf8.RuneCountInString(description) > interfaces.DESCRIPTION_MAX_LENGTH {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Description).
			WithErrorDetails(fmt.Sprintf("The length of the description exceeds %v", interfaces.DESCRIPTION_MAX_LENGTH))
	}
	return nil
}

// Validity verification of pagination parameters
func validatePaginationQueryParams(ctx context.Context, offset, limit, sort, direction string,
	supportedSortTypes map[string]string) (interfaces.PaginationQueryParams, error) {
	pageParams := interfaces.PaginationQueryParams{}

	off, err := strconv.Atoi(offset)
	if err != nil {
		return pageParams, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Offset).
			WithErrorDetails(err.Error())
	}

	if off < interfaces.MIN_OFFSET {
		return pageParams, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Offset).
			WithErrorDetails(fmt.Sprintf("The offset is not greater than %d", interfaces.MIN_OFFSET))
	}

	lim, err := strconv.Atoi(limit)
	if err != nil {
		return pageParams, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Limit).
			WithErrorDetails(err.Error())
	}

	if limit != interfaces.NO_LIMIT && (lim < interfaces.MIN_LIMIT || lim > interfaces.MAX_LIMIT) {
		return pageParams, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Limit).
			WithErrorDetails(fmt.Sprintf("The number per page does not equal %s is not in the range of [%d,%d]", interfaces.NO_LIMIT, interfaces.MIN_LIMIT, interfaces.MAX_LIMIT))
	}

	_, ok := supportedSortTypes[sort]
	if !ok {
		types := make([]string, 0)
		for t := range supportedSortTypes {
			types = append(types, t)
		}
		return pageParams, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Sort).
			WithErrorDetails(fmt.Sprintf("Wrong sort type, does not belong to any item in set %v ", types))
	}

	if direction != interfaces.DESC_DIRECTION && direction != interfaces.ASC_DIRECTION {
		return pageParams, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Direction).
			WithErrorDetails("The sort direction is not desc or asc")
	}

	return interfaces.PaginationQueryParams{
		Offset:    off,
		Limit:     lim,
		Sort:      sort,
		Direction: direction,
	}, nil
}

// parseTaskStatuses verifies and normalizes the status parameter repeatedly passed in the query.
// For multiple states, use status=pending&status=running. Duplicate values are removed in the order of their first occurrence.
func parseTaskStatuses(ctx context.Context, values []string, isValid func(string) bool, errorCode string) ([]string, error) {
	statuses := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		status := strings.TrimSpace(value)
		if status == "" || !isValid(status) {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, errorCode).
				WithErrorDetails(fmt.Sprintf("invalid status: %s", status))
		}
		if _, exists := seen[status]; exists {
			continue
		}
		seen[status] = struct{}{}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

// ConnectorConfig legitimacy verification
func validateConnectorConfig(ctx context.Context, cfg interfaces.ConnectorConfig) error {
	// Check for duplicate elements in databases
	if dbValue, exists := cfg["databases"]; exists {
		if dbArray, ok := dbValue.([]any); ok {
			if err := checkDuplicateElements(ctx, dbArray, "databases"); err != nil {
				return err
			}
		}
	}

	// Check for duplicate elements in schemas
	if schemaValue, exists := cfg["schemas"]; exists {
		if schemaArray, ok := schemaValue.([]any); ok {
			if err := checkDuplicateElements(ctx, schemaArray, "schemas"); err != nil {
				return err
			}
		}
	}

	return nil
}

// Check whether there are duplicate elements in the array
func checkDuplicateElements(ctx context.Context, arr []any, fieldName string) error {
	seen := make(map[string]bool)
	for _, item := range arr {
		strItem := fmt.Sprintf("%v", item)
		if seen[strItem] {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Catalog_InvalidParameter_ConnectorConfig).
				WithErrorDetails(fmt.Sprintf("duplicate element found in '%s': %s", fieldName, strItem))
		}
		seen[strItem] = true
	}
	return nil
}
