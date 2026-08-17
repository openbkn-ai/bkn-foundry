// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package opensearch provides OpenSearch/ElasticSearch connector implementation.
package opensearch

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"

	"vega-backend/interfaces"
)

// GetMetadata returns the metadata for the catalog.
// The GetMetadata method is used to obtain the metadata information of OpenSearch
// Parameter
//
//	-ctx: Context, used to control the timeout and cancellation of requests
//
// Return value:
//
//	-map [string]any: A key-value pair mapping containing OpenSearch metadata
//	-error: If an error occurs during the operation, return the corresponding error message
func (c *OpenSearchConnector) GetMetadata(ctx context.Context) (map[string]any, error) {
	// Check whether the client has initialized the connection
	if c.client == nil {
		return nil, fmt.Errorf("connector not connected")
	}

	// Create an OpenSearch information request
	req := opensearchapi.InfoRequest{}
	// Send a request to the OpenSearch server
	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	// Make sure the response body is closed to release resources
	defer func() { _ = resp.Body.Close() }()
	// Check if the response contains any errors
	if resp.IsError() {
		return nil, fmt.Errorf("get metadata failed: %s", resp.String())
	}

	// It is used to store the metadata information after parsing
	var info map[string]any
	// Decode the JSON data in the response body into the info variable
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	// Return the parsed metadata information
	return info, nil
}

// ListIndexes lists all indices.
func (c *OpenSearchConnector) ListIndexes(ctx context.Context) ([]*interfaces.IndexMeta, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	req := opensearchapi.CatIndicesRequest{
		Format: "json",
	}
	if c.Config.IndexPattern != "" {
		req.Index = []string{c.Config.IndexPattern}
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return nil, fmt.Errorf("failed to list indices: %s", resp.String())
	}

	var catIndices []struct {
		Index     string `json:"index"`
		DocsCount string `json:"docs.count"`
		StoreSize string `json:"store.size"`
	}
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&catIndices); err != nil {
		return nil, err
	}

	var indices []*interfaces.IndexMeta
	for _, idx := range catIndices {
		if strings.HasPrefix(idx.Index, ".") {
			continue // Skip system indices
		}

		indices = append(indices, &interfaces.IndexMeta{
			Name: idx.Index,
			Properties: map[string]any{
				"docs.count": idx.DocsCount,
				"store.size": idx.StoreSize,
			},
		})
	}
	return indices, nil
}

// GetIndexMeta retrieves index metadata (mappings, settings).
// GetIndexMeta obtains the metadata information of the specified index, including mapping and Settings
// Parameter
//
//	-ctx: Context information, used to control the timeout and cancellation of requests
//	-index: A pointer to the interface IndexMeta, used to store the obtained metadata
//
// Return value:
//
//	-error: If an error occurs during the operation, return an error message
func (c *OpenSearchConnector) GetIndexMeta(ctx context.Context, index *interfaces.IndexMeta) error {
	// First, make sure the connector is connected to the OpenSearch service
	if err := c.Connect(ctx); err != nil {
		return err
	}

	// Check if the attribute mapping of the index is empty. If it is empty, initialize an empty map
	if index.Properties == nil {
		index.Properties = make(map[string]any)
	}

	// 1. Get Mappings
	if err := c.fetchMappings(ctx, index); err != nil {
		return fmt.Errorf("failed to fetch mappings: %w", err)
	}

	// 2. Get Settings
	if err := c.fetchSettings(ctx, index); err != nil {
		return fmt.Errorf("failed to fetch settings: %w", err)
	}

	return nil
}

// fetchMappings retrieves and parses index mappings.
func (c *OpenSearchConnector) fetchMappings(ctx context.Context, index *interfaces.IndexMeta) error {
	req := opensearchapi.IndicesGetMappingRequest{
		Index: []string{index.Name},
	}
	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return fmt.Errorf("opensearch API error: %s", resp.String())
	}
	//{
	//	"product_index" : {
	//	"mappings" : {
	//		"properties" : {
	//			"age" : {
	//				"type" : "integer"
	//			},
	//			"create_time" : {
	//				"type" : "date"
	//			},
	//			"description" : {
	//				"type" : "text",
	//				"fields" : {
	//					"keyword" : {
	//						"type" : "keyword",
	//						"ignore_above" : 256
	//					}
	//				}
	//			}
	//		}
	//	}
	//}
	//}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Mapping structure definition
	var dataMapping map[string]struct {
		Mappings struct {
			Properties map[string]Property `json:"properties"`
		} `json:"mappings"`
	}
	// Parse JSON
	err = sonic.Unmarshal(bodyBytes, &dataMapping)
	if err != nil {
		panic(err)
	}

	fieldMap := make(map[string]interfaces.IndexFieldMeta)
	if idxData, ok := dataMapping[index.Name]; ok {
		parseProperties("", idxData.Mappings.Properties, fieldMap)
	}
	index.Mapping = fieldMap
	return nil
}

// Property defines the complete set of field properties.
type Property struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"` // Nested object properties.
	Fields     map[string]Property `json:"fields"`     // multi-fields subfields
	// Use map[string]any to store all other dynamic attributes
	Attributes map[string]any `json:"-"`
}

// UnmarshalJSON custom deserialization method
func (p *Property) UnmarshalJSON(data []byte) error {
	// Parse all fields to a temporary map
	var raw map[string]any
	if err := sonic.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Initialize Attributes
	if p.Attributes == nil {
		p.Attributes = make(map[string]any)
	}

	// Handle the "type" field
	if typeVal, ok := raw["type"]; ok {
		p.Type = fmt.Sprintf("%v", typeVal)
	}

	// Copy all fields except type, properties, and Fields to Attributes
	for key, value := range raw {
		switch key {
		case "properties", "fields":
			continue
		default:
			p.Attributes[key] = value
		}
	}
	// Handle the properties field (recursive parsing)
	if propsVal, ok := raw["properties"].(map[string]any); ok {
		p.Properties = make(map[string]Property)
		for propName, propValue := range propsVal {
			propJSON, _ := sonic.Marshal(propValue)
			var prop Property
			if err := sonic.Unmarshal(propJSON, &prop); err == nil {
				p.Properties[propName] = prop
			}
		}
	}
	// Handle the fields field (recursive parsing)
	if fieldsVal, ok := raw["fields"].(map[string]any); ok {
		p.Fields = make(map[string]Property)
		for fieldName, fieldValue := range fieldsVal {
			fieldJSON, _ := sonic.Marshal(fieldValue)
			var field Property
			if err := sonic.Unmarshal(fieldJSON, &field); err == nil {
				p.Fields[fieldName] = field
			}
		}
	}

	return nil
}

// Recursively parse the field: Flatten the nested object into a dot path and directly produce the IndexFieldMeta;
// multi-fields SubFields are attached to the subfields of their parent fields in alphabetical order. Silently skip non-Object fields without type.
func parseProperties(parentPath string, props map[string]Property, out map[string]interfaces.IndexFieldMeta) {
	for name, prop := range props {
		currentPath := name
		if parentPath != "" {
			currentPath = parentPath + "." + name
		}
		// Only fields that are not object but have a type will result
		if prop.Type != "object" && prop.Type != "" {
			out[currentPath] = interfaces.IndexFieldMeta{
				Name:       currentPath,
				Type:       prop.Type,
				Searchable: true,
				Attributes: prop.Attributes,
				SubFields:  collectSubFields(prop),
			}
		}
		// Recursively parse nested fields of object
		if len(prop.Properties) > 0 {
			parseProperties(currentPath, prop.Properties, out)
		}
	}
}

// collectSubFields extracts the multi-fields subfields in alphabetical order of Name into IndexSubfield meta slices.
// type is stripped from Attributes and inserted into the Type field.
func collectSubFields(p Property) []interfaces.IndexSubFieldMeta {
	if len(p.Fields) == 0 {
		return nil
	}
	subNames := make([]string, 0, len(p.Fields))
	for fieldName := range p.Fields {
		subNames = append(subNames, fieldName)
	}
	sort.Strings(subNames)
	children := make([]interfaces.IndexSubFieldMeta, 0, len(subNames))
	for _, fieldName := range subNames {
		fieldProp := p.Fields[fieldName]
		attrs := make(map[string]any, len(fieldProp.Attributes))
		for k, v := range fieldProp.Attributes {
			if k == "type" {
				continue
			}
			attrs[k] = v
		}
		children = append(children, interfaces.IndexSubFieldMeta{
			Name:       fieldName,
			Type:       fieldProp.Type,
			Attributes: attrs,
		})
	}
	if len(children) == 0 {
		return nil
	}
	return children
}

// fetchSettings retrieves index settings.
func (c *OpenSearchConnector) fetchSettings(ctx context.Context, index *interfaces.IndexMeta) error {
	flatSettings := true
	req := opensearchapi.IndicesGetSettingsRequest{
		Index:        []string{index.Name},
		FlatSettings: &flatSettings,
	}
	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return fmt.Errorf("opensearch API error: %s", resp.String())
	}

	var settingsResp map[string]struct {
		Settings map[string]any `json:"settings"`
	}
	//{
	//	"test-index" : {
	//	"settings" : {
	//		"index.creation_date" : "1772682337114",
	//			"index.number_of_replicas" : "1",
	//			"index.number_of_shards" : "1",
	//			"index.provided_name" : "test-index",
	//			"index.uuid" : "2G4vPna8SIC0vTEzZ0NK3Q",
	//			"index.version.created" : "136287827"
	//	}
	//}
	//}
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&settingsResp); err != nil {
		return err
	}
	if idxData, ok := settingsResp[index.Name]; ok {
		for k, v := range idxData.Settings {
			index.Properties[k] = v
		}
	}
	return nil
}
