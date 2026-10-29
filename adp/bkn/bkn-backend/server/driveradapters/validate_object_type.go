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
	"unicode/utf8"

	"github.com/PaesslerAG/jsonpath"
	"github.com/dlclark/regexp2"
	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/maskrule"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func ValidateObjectTypes(ctx context.Context, knID string, objectTypes []*interfaces.ObjectType, strictMode bool) error {
	tmpNameMap := make(map[string]any)
	idMap := make(map[string]any)
	for i := 0; i < len(objectTypes); i++ {
		// Verify that imported models are object types.
		if objectTypes[i].ModuleType != "" && objectTypes[i].ModuleType != interfaces.MODULE_TYPE_OBJECT_TYPE {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.ModuleType", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_InvalidParameter_ModuleType).
				WithErrorDetails(localizedText)
		}

		// Verify that model IDs in the request are unique.
		otID := objectTypes[i].OTID
		if _, ok := idMap[otID]; !ok || otID == "" {
			idMap[otID] = nil
		} else {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.DuplicatedIDInFile", map[string]any{"id": otID})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			errDetails := localizedText
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_Duplicated_IDInFile).
				WithErrorDetails(errDetails)
		}

		// Validate required object type fields, lengths, and enum values.
		err := ValidateObjectType(ctx, objectTypes[i], strictMode)
		if err != nil {
			return err
		}

		// Verify that object type names in the request are unique.
		if _, ok := tmpNameMap[objectTypes[i].OTName]; !ok {
			tmpNameMap[objectTypes[i].OTName] = nil
		} else {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_Duplicated_Name)
		}

		objectTypes[i].KNID = knID
	}
	return nil
}

// ValidateObjectType validates required object type fields.
// Validation order: basic information, data source, data properties, keys, then logic properties.
func ValidateObjectType(ctx context.Context, objectType *interfaces.ObjectType, strictMode bool) error {
	// Validate basic information: ID, name, and tags.
	if err := validateObjectTypeBasicInfo(ctx, objectType); err != nil {
		return err
	}

	// Validate the data source.
	if err := validateObjectTypeDataSource(ctx, objectType); err != nil {
		return err
	}

	// Validate data properties.
	if err := validateObjectTypeDataProperties(ctx, objectType, strictMode); err != nil {
		return err
	}

	// Build the data property index and validate keys that depend on it.
	dataPropMap := buildDataPropMap(objectType.DataProperties)
	if err := validateObjectTypeKeys(ctx, objectType, dataPropMap, strictMode); err != nil {
		return err
	}

	// Validate logic properties.
	if err := validateObjectTypeLogicProperties(ctx, objectType, strictMode); err != nil {
		return err
	}

	return nil
}

// validateObjectTypeBasicInfo validates an object type ID, name, and tags.
func validateObjectTypeBasicInfo(ctx context.Context, objectType *interfaces.ObjectType) error {
	// Validate the ID.
	if err := validateID(ctx, objectType.OTID); err != nil {
		return err
	}

	// Trim and validate the name.
	objectType.OTName = strings.TrimSpace(objectType.OTName)
	if err := validateObjectName(ctx, objectType.OTName, interfaces.MODULE_TYPE_OBJECT_TYPE); err != nil {
		return err
	}

	// Validate tags.
	if err := ValidateTags(ctx, objectType.Tags); err != nil {
		return err
	}
	// Trim tags and remove duplicates.
	objectType.Tags = libCommon.TagSliceTransform(objectType.Tags)

	return nil
}

// validateObjectTypeDataSource validates an object type data source; only resource is supported.
func validateObjectTypeDataSource(ctx context.Context, objectType *interfaces.ObjectType) error {
	if objectType.DataSource == nil || objectType.DataSource.Type == "" {
		return nil
	}
	if objectType.DataSource.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.DataSourceTypeNotSupported", map[string]any{
			"objectType": objectType.OTName,
			"type":       objectType.DataSource.Type,
		})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
	}
	return nil
}

// buildDataPropMap converts data properties to a map keyed by property name without side effects.
func buildDataPropMap(dataProperties []*interfaces.DataProperty) map[string]*interfaces.DataProperty {
	m := make(map[string]*interfaces.DataProperty, len(dataProperties))
	for _, prop := range dataProperties {
		m[prop.Name] = prop
	}
	return m
}

// validateObjectTypeDataProperties validates the data property count and each property.
func validateObjectTypeDataProperties(ctx context.Context, objectType *interfaces.ObjectType, strictMode bool) error {
	if len(objectType.DataProperties) > interfaces.MAX_PROPERTY_NUM {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.DataPropertiesExceeded", map[string]any{
			"objectType": objectType.OTName,
			"count":      len(objectType.DataProperties),
			"limit":      interfaces.MAX_PROPERTY_NUM,
		})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
	}

	for _, prop := range objectType.DataProperties {
		if err := ValidateDataProperty(ctx, prop, strictMode); err != nil {
			return err
		}
	}
	return nil
}

// validateObjectTypeKeys validates primary, display, and incremental keys using dataPropMap.
// Strict mode requires primary_keys and display_key.
// Non-strict mode allows them to be omitted, but configured keys must reference valid fields.
func validateObjectTypeKeys(ctx context.Context, objectType *interfaces.ObjectType, dataPropMap map[string]*interfaces.DataProperty, strictMode bool) error {
	// Validate primary keys. Strict mode requires them; configured keys must exist and use supported types.
	if len(objectType.PrimaryKeys) == 0 {
		if strictMode {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_NullParameter_PrimaryKeys)
		}
	} else {
		for _, pKey := range objectType.PrimaryKeys {
			prop, ok := dataPropMap[pKey]
			if !ok {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.PrimaryKeyNotFound", map[string]any{"objectType": objectType.OTName, "key": pKey})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(localizedText)
			}
			// Primary key properties support only integer, unsigned integer, string, and text.
			if !interfaces.ValidPrimaryKeyTypes[prop.Type] {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.PrimaryKeyTypeInvalid", map[string]any{"objectType": objectType.OTName, "key": pKey, "type": prop.Type})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
			}
		}
	}

	// Validate the display key. Strict mode requires it; configured keys must exist and use supported types.
	if objectType.DisplayKey == "" {
		if strictMode {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_NullParameter_DisplayKey)
		}
	} else {
		prop, ok := dataPropMap[objectType.DisplayKey]
		if !ok {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.DisplayKeyNotFound", map[string]any{"objectType": objectType.OTName, "key": objectType.DisplayKey})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		// Display keys support integer, unsigned integer, float, decimal, string, text, date, timestamp, time, datetime, and boolean.
		if !interfaces.ValidDisplayKeyTypes[prop.Type] {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.DisplayKeyTypeInvalid", map[string]any{"objectType": objectType.OTName, "key": objectType.DisplayKey, "type": prop.Type})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
		}
	}

	// Validate the incremental key. It is optional, but configured keys must exist and use supported types.
	if objectType.IncrementalKey != "" {
		field, ok := dataPropMap[objectType.IncrementalKey]
		if !ok {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.IncrementalKeyNotFound", map[string]any{"objectType": objectType.OTName, "key": objectType.IncrementalKey})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		switch field.Type {
		case "integer", "datetime", "timestamp":
		default:
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.IncrementalKeyTypeInvalid", map[string]any{"objectType": objectType.OTName, "key": field.Name, "type": field.Type})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
		}
	}

	return nil
}

// validateObjectTypeLogicProperties validates tool-backed logic properties.
func validateObjectTypeLogicProperties(ctx context.Context, objectType *interfaces.ObjectType, strictMode bool) error {
	if len(objectType.LogicProperties) > interfaces.MAX_PROPERTY_NUM {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertiesExceeded", map[string]any{"objectType": objectType.OTName, "count": len(objectType.LogicProperties), "limit": interfaces.MAX_PROPERTY_NUM})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
	}

	dataPropNames := make(map[string]struct{}, len(objectType.DataProperties))
	for _, dp := range objectType.DataProperties {
		dataPropNames[dp.Name] = struct{}{}
	}

	for _, prop := range objectType.LogicProperties {
		// Validate the property name. Its rule differs from ID validation and permits uppercase letters.
		if err := ValidatePropertyName(ctx, prop.Name); err != nil {
			return err
		}
		if _, exists := dataPropNames[prop.Name]; exists {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyDuplicatesDataProperty", map[string]any{"objectType": objectType.OTName, "property": prop.Name})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		// Validate displayName.
		if prop.DisplayName == "" {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyDisplayNameRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		if utf8.RuneCountInString(prop.DisplayName) > interfaces.OBJECT_NAME_MAX_LENGTH {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyDisplayNameTooLong", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "limit": interfaces.OBJECT_NAME_MAX_LENGTH})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		// Type is required and supports only tool-backed logical properties.
		if prop.Type == "" {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyTypeRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
		}
		if prop.Type != interfaces.LOGIC_PROPERTY_TYPE_TOOL {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyTypeInvalid", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "type": prop.Type})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
		}

		// Validate data_source:
		// - tool requires type, box_id, and tool_id;
		// - non-strict mode permits omission, but supplied values must meet the full type-specific contract.
		if prop.DataSource != nil && (prop.DataSource.Type != "" || prop.DataSource.ID != "" ||
			prop.DataSource.BoxID != "" || prop.DataSource.ToolID != "" || prop.DataSource.ResultPath != "") {
			if prop.DataSource.Type == "" {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyDataSourceTypeRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(localizedText)
			}
			if !interfaces.ValidLogicSourceTypes[prop.DataSource.Type] {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyDataSourceTypeInvalid", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "type": prop.DataSource.Type})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(localizedText)
			}
			if prop.Type != prop.DataSource.Type {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyDataSourceTypeMismatch", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "type": prop.Type, "sourceType": prop.DataSource.Type})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
			}
			if prop.DataSource.Type == interfaces.LOGIC_PROPERTY_TYPE_TOOL {
				if prop.DataSource.BoxID == "" || prop.DataSource.ToolID == "" {
					localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyToolSourceRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name})
					if renderErr != nil {
						logger.Errorf("localized message rendering failed: %v", renderErr)
					}
					return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
						WithErrorDetails(localizedText)
				}
			} else if prop.DataSource.ID == "" {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertySourceIDRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
			}
			if prop.DataSource.ResultPath != "" {
				if prop.DataSource.Type != interfaces.LOGIC_PROPERTY_TYPE_TOOL {
					localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyResultPathRequiresTool", map[string]any{"objectType": objectType.OTName, "property": prop.Name})
					if renderErr != nil {
						logger.Errorf("localized message rendering failed: %v", renderErr)
					}
					return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
				}
				if _, err := jsonpath.New(prop.DataSource.ResultPath); err != nil {
					localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyResultPathInvalid", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "error": err.Error()})
					if renderErr != nil {
						logger.Errorf("localized message rendering failed: %v", renderErr)
					}
					// Strict mode rejects a missing data source.
					return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
						WithErrorDetails(localizedText)
				}
			}
		} else if strictMode {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyDataSourceRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			// Strict mode rejects a missing data source.
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
		}

		// Parameter names are required.
		for _, param := range prop.Parameters {
			if param.Name == "" {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyParameterNameRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
			}
			if param.ValueFrom == interfaces.VALUE_FROM_PROPERTY {
				propName, ok := param.Value.(string)
				if !ok || strings.TrimSpace(propName) == "" {
					localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyParameterBindingInvalid", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "parameter": param.Name})
					if renderErr != nil {
						logger.Errorf("localized message rendering failed: %v", renderErr)
					}
					return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
						WithErrorDetails(localizedText)
				}
				if _, exists := dataPropNames[propName]; !exists {
					localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.LogicPropertyParameterPropertyNotFound", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "parameter": param.Name, "dataProperty": propName})
					if renderErr != nil {
						logger.Errorf("localized message rendering failed: %v", renderErr)
					}
					return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
				}
			}
		}

	}

	return nil
}

func ValidatePropertyName(ctx context.Context, name string) error {
	if name == "" {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_NullParameter_PropertyName)
	}
	// Property names contain letters, digits, underscores, and hyphens; they cannot start with an underscore and are limited to 40 characters.
	re := regexp2.MustCompile(interfaces.RegexPattern_Property_Name, regexp2.RE2)
	match, err := re.MatchString(name)
	if err != nil || !match {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.PropertyNameInvalid", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		errDetails := localizedText
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter_PropertyName).
			WithErrorDetails(errDetails)
	}
	return nil
}

func ValidateDataProperties(ctx context.Context, propertyNames []string, dataProperties []*interfaces.DataProperty, strictMode bool) error {
	if len(propertyNames) != len(dataProperties) {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.PropertyNamesLengthMismatch", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(localizedText)
		return httpErr
	}

	propertyNameMap := map[string]string{}
	for _, propertyName := range propertyNames {
		propertyNameMap[propertyName] = propertyName
	}
	for _, prop := range dataProperties {
		if _, ok := propertyNameMap[prop.Name]; !ok {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.DataPropertyMissingFromURL", map[string]any{"property": prop.Name})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(localizedText)
			return httpErr
		}

		err := ValidateDataProperty(ctx, prop, strictMode)
		if err != nil {
			return err
		}
	}
	return nil
}

func ValidateDataProperty(ctx context.Context, dataProperty *interfaces.DataProperty, strictMode bool) error {
	// Validate the property name. Unlike an ID, it also supports uppercase letters.
	err := ValidatePropertyName(ctx, dataProperty.Name)
	if err != nil {
		return err
	}

	if dataProperty.DisplayName == "" {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.DataPropertyDisplayNameRequired", map[string]any{"property": dataProperty.Name})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	if utf8.RuneCountInString(dataProperty.DisplayName) > interfaces.OBJECT_NAME_MAX_LENGTH {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.DataPropertyDisplayNameTooLong", map[string]any{"property": dataProperty.Name, "limit": interfaces.OBJECT_NAME_MAX_LENGTH})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
	}

	// When data_property.type is set, it must be a supported type: integer, unsigned integer, float, decimal, string, keyword, text, date, timestamp, time, datetime, boolean, binary, json, vector, point, shape, or ip.
	if dataProperty.Type != "" {
		if !interfaces.ValidDataPropertyTypes[dataProperty.Type] {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.DataPropertyTypeInvalid", map[string]any{"property": dataProperty.Name, "type": dataProperty.Type})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
		}
	}
	if err := maskrule.Validate(dataProperty.Type, dataProperty.MaskRule); err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.DataPropertyMaskRuleInvalid", map[string]any{"property": dataProperty.Name, "reason": err.Error()})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	// When data_property.mapped_field is set, name is required.
	if dataProperty.MappedField != nil && dataProperty.MappedField.Name == "" {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.DataPropertyMappedFieldRequired", map[string]any{"property": dataProperty.Name})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	if dataProperty.HasRetiredIndexConfig() {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.DataPropertyIndexConfigUnsupported", map[string]any{"property": dataProperty.Name})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
	}
	return nil
}
