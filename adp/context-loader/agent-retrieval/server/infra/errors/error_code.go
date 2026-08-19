// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package errors define error codes.
// @file errors_code.go
// @description: Define error code.
package errors

// common extended error code definition.
const (
	ErrExtCommonOperationForbidden      = "CommonOperationForbidden"      // No operation permission.
	ErrExtCommonAddForbidden            = "CommonAddForbidden"            // No new permission.
	ErrExtCommonEditForbidden           = "CommonEditForbidden"           // No editing rights.
	ErrExtCommonDeleteForbidden         = "CommonDeleteForbidden"         // No delete permission.
	ErrExtCommonPublishForbidden        = "CommonPublishForbidden"        // No publishing permission.
	ErrExtCommonUnpublishForbidden      = "CommonUnpublishForbidden"      // No removal permission.
	ErrExtCommonPermissionForbidden     = "CommonPermissionForbidden"     // No permission management permissions.
	ErrExtCommonPublicAccessForbidden   = "CommonPublicAccessForbidden"   // No public access.
	ErrExtCommonUseForbidden            = "CommonUseForbidden"            // No permission to use.
	ErrExtCommonViewForbidden           = "CommonViewForbidden"           // No viewing permission.
	ErrExtCommonUserNotFound            = "CommonUserNotFound"            // User does not exist.
	ErrExtCommonAnonymousUserNotAllowed = "CommonAnonymousUserNotAllowed" // Anonymous users are not allowed access.
	ErrExtCommonExternalServerError     = "CommonExternalServerError"     // Externalserviceexception.
)

// MCP extended error code definition.
const (
	ErrExtMCPInstanceAlreadyExists = "MCPInstanceAlreadyExists" // MCP instance already exists.
	ErrExtMCPInstanceNotFound      = "MCPInstanceNotFound"      // MCPinstancedoes not exist.
	ErrExtMCPInfoBuildFailed       = "MCPInfoBuildFailed"       // MCP information construction failed.
	ErrExtMCPPTCUnavailable        = "MCPPTCUnavailable"        // PTC MCP endpoint is unavailable
	ErrExtMCPPTCToolkitBuildFailed = "MCPPTCToolkitBuildFailed" // PTC MCP toolkit build failed
)

// Business knowledge network action recall expansion error code definition.
const (
	ErrExtKnActionRecallUnsupportedType     = "KnActionRecallUnsupportedType"     // Unsupported action source type.
	ErrExtKnActionRecallNoActionsFound      = "KnActionRecallNoActionsFound"      // No available actions found.
	ErrExtKnActionRecallSchemaConvertFailed = "KnActionRecallSchemaConvertFailed" // Schemaconvertfailure.
	ErrExtKnActionRecallToolNotFound        = "KnActionRecallToolNotFound"        // Tooldoes not exist.
)

// Common error code definitions.
const (
	ErrExtCommonNameInvalid = "CommonNameInvalid" // Only supports input of Chinese characters, letters, numbers, underlines or spaces.
)

// Validator error code definition.
const (
	ErrExtCodeValidationRequired = "ValidationRequired" // Required fields.
	ErrExtCodeValidationFormat   = "ValidationFormat"   // Format error.
	ErrExtCodeValidationRange    = "ValidationRange"    // Range error.
	ErrExtCodeValidationEnum     = "ValidationEnum"     // Enumeration error.
)

const (
	CommonSolution = "Common"
	NoneSolution   = "None"
)

const (
	NoneErrorLink = "None"
)
