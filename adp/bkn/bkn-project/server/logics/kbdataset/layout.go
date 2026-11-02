// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kbdataset prepares document-model storage without persisting instances.
package kbdataset

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var ErrInvalidLayout = errors.New("document Dataset model is not ready for storage")

// StorageVersion invalidates preparation plans when server-owned provenance
// columns change, including for drafts confirmed by an earlier server version.
const StorageVersion = 2

// Field is a physical Vega field. Its name can differ from a model property;
// binding keeps that explicit rather than exposing index metadata as a field.
type Field = interfaces.DatasetField

type Layout = interfaces.DatasetLayout

var fieldName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

func storageType(typ string) bool {
	switch typ {
	case "string", "text", "integer", "unsigned integer", "float", "decimal", "boolean", "date", "time", "datetime", "timestamp", "json":
		return true
	default:
		return false
	}
}

// BuildLayout is deliberately independent of a remote resource id. The durable
// preparation journal assigns ids before any remote create, then uses this
// layout to verify existing resources after an uncertain/partially failed create.
func BuildLayout(object *interfaces.DraftObjectType) (*Layout, error) {
	if object == nil || !object.Adopted || object.Document == nil || object.NeedsIdentity || len(object.PrimaryKeys) == 0 || len(object.Properties) == 0 {
		return nil, ErrInvalidLayout
	}
	switch object.Document.Role {
	case "entity", "file", "in_document", "fact", "event", "evidence_document", "evidence_block", "evidence_image", "evidence_media", "evidence_link":
	default:
		return nil, ErrInvalidLayout
	}
	if object.Document.Role != "entity" && !slices.Equal(object.PrimaryKeys, []string{"_id"}) {
		return nil, ErrInvalidLayout
	}
	properties := map[string]*interfaces.DraftProperty{}
	for _, property := range object.Properties {
		if property == nil || properties[property.Name] != nil || !storageType(property.Type) || (property.Name != "_id" && !fieldName.MatchString(property.Name)) {
			return nil, ErrInvalidLayout
		}
		if property.Name == "_id" && (!property.ReadOnly || property.Type != "string") {
			return nil, ErrInvalidLayout
		}
		properties[property.Name] = property
	}
	if properties["_id"] == nil {
		return nil, ErrInvalidLayout
	}
	for _, meta := range []struct{ name, label string }{{"model_fingerprint", "Confirmed model version"}, {"extraction_version", "Extraction version"}} {
		if p := properties[meta.name]; p != nil {
			if !p.ReadOnly || p.Type != "string" || p.Extraction != nil {
				return nil, ErrInvalidLayout
			}
		} else {
			// Legacy logical models retain their human decisions. The Dataset
			// still gets the provenance columns required by every new build.
			properties[meta.name] = &interfaces.DraftProperty{Name: meta.name, DisplayName: meta.label, Type: "string", ReadOnly: true, SourceOfRecord: "document"}
		}
	}
	// Vega consumes write _id as OpenSearch metadata and removes it from _source.
	// A regular field mirrors that same stable id for schema/query/key mapping.
	// Keep user fields intact even when one already has the preferred alias.
	idField := "bkn_record_id"
	for suffix := 2; properties[idField] != nil; suffix++ {
		idField = "bkn_record_id_" + strconv.Itoa(suffix)
	}
	layout := &Layout{Mapping: map[string]string{}, Fields: []Field{}, PrimaryKeys: []string{}}
	for name, property := range properties {
		physical := name
		if name == "_id" {
			physical = idField
		}
		layout.Mapping[name] = physical
		label := property.DisplayName
		if label == "" {
			label = name
		}
		layout.Fields = append(layout.Fields, Field{Name: physical, Type: property.Type, DisplayName: label, OriginalName: name, Description: property.Description})
	}
	slices.SortFunc(layout.Fields, func(a, b Field) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	seenKeys := map[string]bool{}
	for _, key := range object.PrimaryKeys {
		property := properties[key]
		if property == nil || seenKeys[key] {
			return nil, ErrInvalidLayout
		}
		if object.Document.Role == "entity" && (property.ReadOnly || (property.Type != "string" && property.Type != "text" && property.Type != "integer" && property.Type != "unsigned integer")) {
			return nil, ErrInvalidLayout
		}
		seenKeys[key] = true
		layout.PrimaryKeys = append(layout.PrimaryKeys, layout.Mapping[key])
	}
	encoded, err := json.Marshal(struct {
		Version int      `json:"version"`
		Role    string   `json:"role"`
		Fields  []Field  `json:"fields"`
		Keys    []string `json:"keys"`
	}{StorageVersion, object.Document.Role, layout.Fields, layout.PrimaryKeys})
	if err != nil {
		return nil, ErrInvalidLayout
	}
	digest := sha256.Sum256(encoded)
	layout.Fingerprint = hex.EncodeToString(digest[:])
	return layout, nil
}
