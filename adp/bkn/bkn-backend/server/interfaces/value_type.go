// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"database/sql"
)

// ValueType is a BKN spec 3.0 value type (SPECIFICATION.md §16): a base type with a set of value
// rules, named so that several data properties can share it through constraints.value_type. It
// has no authorization of its own; whoever may view or modify the knowledge network may view or
// modify its value types.
type ValueType struct {
	VTID       string `json:"id" mapstructure:"id"`
	VTName     string `json:"name" mapstructure:"name"`
	CommonInfo `mapstructure:",squash"`
	KNID       string `json:"kn_id" mapstructure:"kn_id"`
	Branch     string `json:"branch" mapstructure:"branch"`

	BaseType            string `json:"base_type" mapstructure:"base_type"`
	ValueTypeDefinition `mapstructure:",squash"`

	Creator    AccountInfo `json:"creator" mapstructure:"creator"`
	CreateTime int64       `json:"create_time" mapstructure:"create_time"`
	Updater    AccountInfo `json:"updater" mapstructure:"updater"`
	UpdateTime int64       `json:"update_time" mapstructure:"update_time"`
	ModuleType string      `json:"module_type" mapstructure:"module_type"`
}

// ValueTypeDefinition holds the value rules of a value type, stored as f_definition. An empty
// field is not declared; an enum goes with none of range, length and pattern.
type ValueTypeDefinition struct {
	Enum    []*ValueTypeEnumItem `json:"enum,omitempty" mapstructure:"enum,omitempty"`
	Range   *ValueRange          `json:"range,omitempty" mapstructure:"range,omitempty"`
	Length  *LengthRange         `json:"length,omitempty" mapstructure:"length,omitempty"`
	Pattern string               `json:"pattern,omitempty" mapstructure:"pattern,omitempty"`
	Unit    string               `json:"unit,omitempty" mapstructure:"unit,omitempty"`
}

// ValueTypeEnumItem is one enum item. Value is written as text whatever the base type; the base
// type says whether it is a number, a date or text.
type ValueTypeEnumItem struct {
	Value       string `json:"value" mapstructure:"value"`
	Display     string `json:"display" mapstructure:"display"`
	Deprecated  bool   `json:"deprecated,omitempty" mapstructure:"deprecated,omitempty"`
	Description string `json:"description,omitempty" mapstructure:"description,omitempty"`
}

// ValueTypeReference is a data property whose constraints name a value type.
type ValueTypeReference struct {
	ObjectTypeID   string `json:"object_type_id"`
	ObjectTypeName string `json:"object_type_name"`
	Property       string `json:"property"`
	PropertyType   string `json:"property_type"`
}

type ValueTypesQueryParams struct {
	PaginationQueryParameters
	NamePattern string
	Tag         string
	Branch      string
	KNID        string
}

var ValueTypeSort = map[string]string{
	"name":        "f_name",
	"update_time": "f_update_time",
}

//go:generate mockgen -source value_type.go -destination mock/mock_value_type.go
type ValueTypeAccess interface {
	GetValueTypeIdentitiesByIDsOrNames(ctx context.Context, knID string, branch string, ids, names []string) ([]*ValueType, error)
	CreateValueTypes(ctx context.Context, tx *sql.Tx, valueTypes []*ValueType) error
	ListValueTypes(ctx context.Context, query ValueTypesQueryParams) ([]*ValueType, error)
	GetValueTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, ids []string) ([]*ValueType, error)
	UpdateValueType(ctx context.Context, tx *sql.Tx, valueType *ValueType) error
	DeleteValueTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, ids []string) (int64, error)
	DeleteValueTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) (int64, error)
}

type ValueTypeService interface {
	CreateValueTypes(ctx context.Context, tx *sql.Tx, valueTypes []*ValueType, mode string) ([]string, error)
	ListValueTypes(ctx context.Context, query ValueTypesQueryParams) ([]*ValueType, int, error)
	GetValueTypeByID(ctx context.Context, knID string, branch string, vtID string) (*ValueType, error)
	UpdateValueType(ctx context.Context, tx *sql.Tx, valueType *ValueType) error
	DeleteValueType(ctx context.Context, tx *sql.Tx, knID string, branch string, vtID string) error
	GetAllValueTypesByKnID(ctx context.Context, knID string, branch string) ([]*ValueType, error)
	DeleteValueTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) error
}
