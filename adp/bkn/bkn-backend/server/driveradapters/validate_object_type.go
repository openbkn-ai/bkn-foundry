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
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func ValidateObjectTypes(ctx context.Context, knID string, objectTypes []*interfaces.ObjectType, strictMode bool) error {
	tmpNameMap := make(map[string]any)
	idMap := make(map[string]any)
	for i := 0; i < len(objectTypes); i++ {
		// Verify that imported models are object types.
		if objectTypes[i].ModuleType != "" && objectTypes[i].ModuleType != interfaces.MODULE_TYPE_OBJECT_TYPE {
			return rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_InvalidParameter_ModuleType).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "ModuleType", nil))
		}

		// Verify that model IDs in the request are unique.
		otID := objectTypes[i].OTID
		if _, ok := idMap[otID]; !ok || otID == "" {
			idMap[otID] = nil
		} else {
			errDetails := objectTypeInvalidDetail(ctx, "DuplicatedIDInFile", map[string]any{"id": otID})
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_Duplicated_IDInFile).
				WithDescription(map[string]any{"ObjectTypeID": otID}).
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
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_Duplicated_Name)
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
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(objectTypeInvalidDetail(ctx, "DataSourceTypeNotSupported", map[string]any{
				"objectType": objectType.OTName,
				"type":       objectType.DataSource.Type,
			}))
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
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(objectTypeInvalidDetail(ctx, "DataPropertiesExceeded", map[string]any{
				"objectType": objectType.OTName,
				"count":      len(objectType.DataProperties),
				"limit":      interfaces.MAX_PROPERTY_NUM,
			}))
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
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_NullParameter_PrimaryKeys)
		}
	} else {
		for _, pKey := range objectType.PrimaryKeys {
			prop, ok := dataPropMap[pKey]
			if !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(objectTypeInvalidDetail(ctx, "PrimaryKeyNotFound", map[string]any{"objectType": objectType.OTName, "key": pKey}))
			}
			// Primary key properties support only integer, unsigned integer, string, and text.
			if !interfaces.ValidPrimaryKeyTypes[prop.Type] {
				return rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(objectTypeInvalidDetail(ctx, "PrimaryKeyTypeInvalid", map[string]any{"objectType": objectType.OTName, "key": pKey, "type": prop.Type}))
			}
		}
	}

	// Validate the display key. Strict mode requires it; configured keys must exist and use supported types.
	if objectType.DisplayKey == "" {
		if strictMode {
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_NullParameter_DisplayKey)
		}
	} else {
		prop, ok := dataPropMap[objectType.DisplayKey]
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "DisplayKeyNotFound", map[string]any{"objectType": objectType.OTName, "key": objectType.DisplayKey}))
		}
		// Display keys support integer, unsigned integer, float, decimal, string, text, date, timestamp, time, datetime, and boolean.
		if !interfaces.ValidDisplayKeyTypes[prop.Type] {
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "DisplayKeyTypeInvalid", map[string]any{"objectType": objectType.OTName, "key": objectType.DisplayKey, "type": prop.Type}))
		}
	}

	// Validate the incremental key. It is optional, but configured keys must exist and use supported types.
	if objectType.IncrementalKey != "" {
		field, ok := dataPropMap[objectType.IncrementalKey]
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "IncrementalKeyNotFound", map[string]any{"objectType": objectType.OTName, "key": objectType.IncrementalKey}))
		}
		switch field.Type {
		case "integer", "datetime", "timestamp":
		default:
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "IncrementalKeyTypeInvalid", map[string]any{"objectType": objectType.OTName, "key": field.Name, "type": field.Type}))
		}
	}

	return nil
}

// validateObjectTypeLogicProperties validates the number and content of logic properties.
// It also adds instant, start, end, and step system parameters for metric properties.
func validateObjectTypeLogicProperties(ctx context.Context, objectType *interfaces.ObjectType, strictMode bool) error {
	if len(objectType.LogicProperties) > interfaces.MAX_PROPERTY_NUM {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertiesExceeded", map[string]any{"objectType": objectType.OTName, "count": len(objectType.LogicProperties), "limit": interfaces.MAX_PROPERTY_NUM}))
	}

	dataPropNames := make(map[string]struct{}, len(objectType.DataProperties))
	for _, dp := range objectType.DataProperties {
		dataPropNames[dp.Name] = struct{}{}
	}

	ifSystemGen := true
	for i, prop := range objectType.LogicProperties {
		// Validate the property name. Its rule differs from ID validation and permits uppercase letters.
		if err := ValidatePropertyName(ctx, prop.Name); err != nil {
			return err
		}
		if _, exists := dataPropNames[prop.Name]; exists {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyDuplicatesDataProperty", map[string]any{"objectType": objectType.OTName, "property": prop.Name}))
		}

		// Validate displayName.
		if prop.DisplayName == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyDisplayNameRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name}))
		}
		if utf8.RuneCountInString(prop.DisplayName) > interfaces.OBJECT_NAME_MAX_LENGTH {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyDisplayNameTooLong", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "limit": interfaces.OBJECT_NAME_MAX_LENGTH}))
		}

		// Type is required and supports only metric or tool.
		if prop.Type == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyTypeRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name}))
		}
		if prop.Type != interfaces.LOGIC_PROPERTY_TYPE_METRIC &&
			prop.Type != interfaces.LOGIC_PROPERTY_TYPE_TOOL {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyTypeInvalid", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "type": prop.Type}))
		}

		// Validate data_source:
		// - metric requires type and ID;
		// - tool requires type, box_id, and tool_id;
		// - non-strict mode permits omission, but supplied values must meet the full type-specific contract.
		if prop.DataSource != nil && (prop.DataSource.Type != "" || prop.DataSource.ID != "" ||
			prop.DataSource.BoxID != "" || prop.DataSource.ToolID != "" || prop.DataSource.ResultPath != "") {
			if prop.DataSource.Type == "" {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyDataSourceTypeRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name}))
			}
			if !interfaces.ValidLogicSourceTypes[prop.DataSource.Type] {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyDataSourceTypeInvalid", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "type": prop.DataSource.Type}))
			}
			if prop.Type != prop.DataSource.Type {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyDataSourceTypeMismatch", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "type": prop.Type, "sourceType": prop.DataSource.Type}))
			}
			if prop.DataSource.Type == interfaces.LOGIC_PROPERTY_TYPE_TOOL {
				if prop.DataSource.BoxID == "" || prop.DataSource.ToolID == "" {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
						WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyToolSourceRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name}))
				}
			} else if prop.DataSource.ID == "" {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertySourceIDRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name}))
			}
			if prop.DataSource.ResultPath != "" {
				if prop.DataSource.Type != interfaces.LOGIC_PROPERTY_TYPE_TOOL {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
						WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyResultPathRequiresTool", map[string]any{"objectType": objectType.OTName, "property": prop.Name}))
				}
				if _, err := jsonpath.New(prop.DataSource.ResultPath); err != nil {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
						WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyResultPathInvalid", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "error": err.Error()}))
				}
			}
		} else if strictMode {
			// Strict mode rejects a missing data source.
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyDataSourceRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name}))
		}

		// Parameter names are required.
		for _, param := range prop.Parameters {
			if param.Name == "" {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyParameterNameRequired", map[string]any{"objectType": objectType.OTName, "property": prop.Name}))
			}
			if param.ValueFrom == interfaces.VALUE_FROM_PROPERTY {
				propName, ok := param.Value.(string)
				if !ok || strings.TrimSpace(propName) == "" {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
						WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyParameterBindingInvalid", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "parameter": param.Name}))
				}
				if _, exists := dataPropNames[propName]; !exists {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
						WithErrorDetails(objectTypeInvalidDetail(ctx, "LogicPropertyParameterPropertyNotFound", map[string]any{"objectType": objectType.OTName, "property": prop.Name, "parameter": param.Name, "dataProperty": propName}))
				}
			}
		}

		// Metric properties automatically receive instant, start, end, and step system parameters.
		if prop.Type == interfaces.LOGIC_PROPERTY_TYPE_METRIC {
			paramMap := make(map[string]struct{}, len(prop.Parameters))
			for _, param := range prop.Parameters {
				paramMap[param.Name] = struct{}{}
			}
			var extra []interfaces.Parameter
			for _, name := range []string{"instant", "start", "end", "step"} {
				if _, exists := paramMap[name]; exists {
					continue
				}
				p := interfaces.Parameter{
					Operation:   "==",
					ValueFrom:   interfaces.VALUE_FROM_INPUT,
					IfSystemGen: &ifSystemGen,
					Name:        name,
				}
				switch name {
				case "instant":
					p.Type = "boolean"
				case "start", "end":
					p.Type = "integer"
				case "step":
					p.Type = "string"
				}
				extra = append(extra, p)
			}
			objectType.LogicProperties[i].Parameters = append(objectType.LogicProperties[i].Parameters, extra...)
		}
	}

	return nil
}

func ValidatePropertyName(ctx context.Context, name string) error {
	if name == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_NullParameter_PropertyName)
	}
	// IDs contain letters, digits, underscores, and hyphens; they cannot start with an underscore and are limited to 40 characters.
	re := regexp2.MustCompile(interfaces.RegexPattern_Property_Name, regexp2.RE2)
	match, err := re.MatchString(name)
	if err != nil || !match {
		errDetails := objectTypeInvalidDetail(ctx, "PropertyNameInvalid", nil)
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter_PropertyName).
			WithErrorDetails(errDetails)
	}
	return nil
}

func ValidateDataProperties(ctx context.Context, propertyNames []string, dataProperties []*interfaces.DataProperty, strictMode bool) error {
	if len(propertyNames) != len(dataProperties) {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(objectTypeInvalidDetail(ctx, "PropertyNamesLengthMismatch", nil))
		return httpErr
	}

	propertyNameMap := map[string]string{}
	for _, propertyName := range propertyNames {
		propertyNameMap[propertyName] = propertyName
	}
	for _, prop := range dataProperties {
		if _, ok := propertyNameMap[prop.Name]; !ok {
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "DataPropertyMissingFromURL", map[string]any{"property": prop.Name}))
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
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(objectTypeInvalidDetail(ctx, "DataPropertyDisplayNameRequired", map[string]any{"property": dataProperty.Name}))
	}
	if utf8.RuneCountInString(dataProperty.DisplayName) > interfaces.OBJECT_NAME_MAX_LENGTH {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(objectTypeInvalidDetail(ctx, "DataPropertyDisplayNameTooLong", map[string]any{"property": dataProperty.Name, "limit": interfaces.OBJECT_NAME_MAX_LENGTH}))
	}

	// When data_property.type is set, it must be a supported type: integer, unsigned integer, float, decimal, string, text, date, timestamp, time, datetime, boolean, binary, json, vector, point, shape, or ip.
	if dataProperty.Type != "" {
		if !interfaces.ValidDataPropertyTypes[dataProperty.Type] {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(objectTypeInvalidDetail(ctx, "DataPropertyTypeInvalid", map[string]any{"property": dataProperty.Name, "type": dataProperty.Type}))
		}
	}

	// When data_property.mapped_field is set, name is required.
	if dataProperty.MappedField != nil && dataProperty.MappedField.Name == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(objectTypeInvalidDetail(ctx, "DataPropertyMappedFieldRequired", map[string]any{"property": dataProperty.Name}))
	}

	if dataProperty.HasRetiredIndexConfig() {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(objectTypeInvalidDetail(ctx, "DataPropertyIndexConfigUnsupported", map[string]any{"property": dataProperty.Name}))
	}

	return nil
}

func objectTypeInvalidDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail."+name, templateData)
}
