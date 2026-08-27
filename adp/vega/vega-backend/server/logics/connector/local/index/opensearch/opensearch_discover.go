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
	"strconv"
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
	var indexNames []string
	if c.Config.IndexPattern != "" {
		indexNames = []string{c.Config.IndexPattern}
	}
	return c.listIndexes(ctx, indexNames)
}

func (c *OpenSearchConnector) listIndexes(ctx context.Context, indexNames []string) ([]*interfaces.IndexMeta, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	req := opensearchapi.CatIndicesRequest{
		Index:  indexNames,
		Format: "json",
		H:      []string{"index", "docs.count", "store.size", "creation.date"},
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
		Index        string `json:"index"`
		DocsCount    string `json:"docs.count"`
		StoreSize    string `json:"store.size"`
		CreationDate string `json:"creation.date"`
	}
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&catIndices); err != nil {
		return nil, err
	}

	var indices []*interfaces.IndexMeta
	for _, idx := range catIndices {
		if strings.HasPrefix(idx.Index, ".") {
			continue // Skip system indices
		}

		creationTime, err := strconv.ParseInt(idx.CreationDate, 10, 64)
		if err != nil {
			creationTime = 0
		}
		indices = append(indices, &interfaces.IndexMeta{
			Name:         idx.Index,
			CreationTime: creationTime,
			Description:  "",
			MappingMeta:  map[string]any{},
			Properties: map[string]any{
				"docs.count": idx.DocsCount,
				"store.size": idx.StoreSize,
			},
		})
	}
	return indices, nil
}

// GetIndexMeta retrieves an index's mappings and settings.
func (c *OpenSearchConnector) GetIndexMeta(ctx context.Context, index *interfaces.IndexMeta) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	if index.Properties == nil {
		index.Properties = make(map[string]any)
	}
	index.Description = ""
	index.MappingMeta = make(map[string]any)

	if err := c.fetchMappings(ctx, index); err != nil {
		return fmt.Errorf("failed to fetch mappings: %w", err)
	}

	if err := c.fetchSettings(ctx, index); err != nil {
		return fmt.Errorf("failed to fetch settings: %w", err)
	}

	return nil
}

func (c *OpenSearchConnector) GetIndexMetaByIdentifier(ctx context.Context, sourceIdentifier string) (*interfaces.IndexMeta, error) {
	indices, err := c.listIndexes(ctx, []string{sourceIdentifier})
	if err != nil {
		return nil, fmt.Errorf("list indexes: %w", err)
	}

	var index *interfaces.IndexMeta
	for _, candidate := range indices {
		if candidate.Name == sourceIdentifier {
			index = candidate
			break
		}
	}
	if index == nil {
		return nil, fmt.Errorf("index %q not found", sourceIdentifier)
	}

	if err := c.GetIndexMeta(ctx, index); err != nil {
		return nil, err
	}
	return index, nil
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
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var dataMapping map[string]struct {
		Mappings struct {
			Meta       map[string]any      `json:"_meta"`
			Properties map[string]Property `json:"properties"`
		} `json:"mappings"`
	}
	err = sonic.Unmarshal(bodyBytes, &dataMapping)
	if err != nil {
		return fmt.Errorf("failed to parse mappings: %w", err)
	}

	fieldMap := make(map[string]interfaces.IndexFieldMeta)
	index.MappingMeta = make(map[string]any)
	if idxData, ok := dataMapping[index.Name]; ok {
		if idxData.Mappings.Meta != nil {
			index.MappingMeta = idxData.Mappings.Meta
		}
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
	var raw map[string]any
	if err := sonic.Unmarshal(data, &raw); err != nil {
		return err
	}

	if p.Attributes == nil {
		p.Attributes = make(map[string]any)
	}

	if typeVal, ok := raw["type"]; ok {
		p.Type = fmt.Sprintf("%v", typeVal)
	}

	// Preserve mapping parameters other than nested and multi-field definitions.
	for key, value := range raw {
		switch key {
		case "properties", "fields":
			continue
		default:
			p.Attributes[key] = value
		}
	}
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

// parseProperties flattens nested objects and retains multi-fields on their parent fields.
func parseProperties(parentPath string, props map[string]Property, out map[string]interfaces.IndexFieldMeta) {
	for name, prop := range props {
		currentPath := name
		if parentPath != "" {
			currentPath = parentPath + "." + name
		}
		if prop.Type != "object" && prop.Type != "" {
			out[currentPath] = interfaces.IndexFieldMeta{
				Name:        currentPath,
				Description: descriptionFromFieldMeta(prop.Attributes),
				Type:        prop.Type,
				Searchable:  isSearchable(prop.Attributes),
				Attributes:  prop.Attributes,
				SubFields:   collectSubFields(prop),
			}
		}
		if len(prop.Properties) > 0 {
			parseProperties(currentPath, prop.Properties, out)
		}
	}
}

func descriptionFromFieldMeta(attributes map[string]any) string {
	meta, ok := attributes["meta"].(map[string]any)
	if !ok {
		return ""
	}
	description, _ := meta["description"].(string)
	return description
}

func isSearchable(attributes map[string]any) bool {
	searchable, ok := attributes["index"].(bool)
	return !ok || searchable
}

// collectSubFields returns multi-fields in name order for stable serialization.
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
