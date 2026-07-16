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
// GetMetadata 方法用于获取OpenSearch的元数据信息
// 参数:
//   - ctx: 上下文，用于控制请求的超时和取消
//
// 返回值:
//   - map[string]any: 包含OpenSearch元数据的键值对映射
//   - error: 如果操作过程中发生错误，返回相应的错误信息
func (c *OpenSearchConnector) GetMetadata(ctx context.Context) (map[string]any, error) {
	// 检查客户端是否已初始化连接
	if c.client == nil {
		return nil, fmt.Errorf("connector not connected")
	}

	// 创建OpenSearch信息请求
	req := opensearchapi.InfoRequest{}
	// 发送请求到OpenSearch服务器
	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	// 确保响应体被关闭，以释放资源
	defer func() { _ = resp.Body.Close() }()
	// 检查响应是否包含错误
	if resp.IsError() {
		return nil, fmt.Errorf("get metadata failed: %s", resp.String())
	}

	// 用于存储解析后的元数据信息
	var info map[string]any
	// 将响应体中的JSON数据解码到info变量中
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	// 返回解析后的元数据信息
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
// GetIndexMeta 获取指定索引的元数据信息，包括映射和设置
// 参数:
//   - ctx: 上下文信息，用于控制请求的超时和取消
//   - index: 指向接口 IndexMeta 的指针，用于存储获取到的元数据
//
// 返回值:
//   - error: 如果操作过程中发生错误，则返回错误信息
func (c *OpenSearchConnector) GetIndexMeta(ctx context.Context, index *interfaces.IndexMeta) error {
	// 首先确保连接器已连接到 OpenSearch 服务
	if err := c.Connect(ctx); err != nil {
		return err
	}

	// 检查索引的属性映射是否为空，如果为空则初始化一个空的 map
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

	// 映射结构定义
	var dataMapping map[string]struct {
		Mappings struct {
			Properties map[string]Property `json:"properties"`
		} `json:"mappings"`
	}
	// 解析 JSON
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

// Property 定义完整的字段属性
type Property struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"` // object 嵌套
	Fields     map[string]Property `json:"fields"`     // multi-fields 子字段
	// 使用 map[string]any 存储所有其他动态属性
	Attributes map[string]any `json:"-"`
}

// UnmarshalJSON 自定义反序列化方法
func (p *Property) UnmarshalJSON(data []byte) error {
	// 解析所有字段到一个临时的 map
	var raw map[string]any
	if err := sonic.Unmarshal(data, &raw); err != nil {
		return err
	}

	// 初始化 Attributes
	if p.Attributes == nil {
		p.Attributes = make(map[string]any)
	}

	// 处理 type 字段
	if typeVal, ok := raw["type"]; ok {
		p.Type = fmt.Sprintf("%v", typeVal)
	}

	// 将除 type、properties、fields 之外的所有字段复制到 Attributes
	for key, value := range raw {
		switch key {
		case "properties", "fields":
			continue
		default:
			p.Attributes[key] = value
		}
	}
	// 处理 properties 字段（递归解析）
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
	// 处理 fields 字段（递归解析）
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

// 递归解析字段：object 嵌套扁平化为点号路径并直接产出 IndexFieldMeta；
// multi-fields 子字段按字母序挂到所在父字段的 SubFields 上；无 type 的非 object 字段静默跳过。
func parseProperties(parentPath string, props map[string]Property, out map[string]interfaces.IndexFieldMeta) {
	for name, prop := range props {
		currentPath := name
		if parentPath != "" {
			currentPath = parentPath + "." + name
		}
		// 非 object 且有 type 的字段才落到结果
		if prop.Type != "object" && prop.Type != "" {
			out[currentPath] = interfaces.IndexFieldMeta{
				Name:       currentPath,
				Type:       prop.Type,
				Searchable: true,
				Attributes: prop.Attributes,
				SubFields:  collectSubFields(prop),
			}
		}
		// 递归解析 object 嵌套字段
		if len(prop.Properties) > 0 {
			parseProperties(currentPath, prop.Properties, out)
		}
	}
}

// collectSubFields 将 multi-fields 子字段按 Name 字母序提取为 IndexSubFieldMeta 切片。
// type 从 Attributes 剥离塞入 Type 字段。
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
