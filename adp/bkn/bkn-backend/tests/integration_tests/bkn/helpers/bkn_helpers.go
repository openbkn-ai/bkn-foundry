// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package helpers

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bkn-backend-tests/testutil"
)

func IsValidTar(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	reader := bytes.NewReader(data)
	tarReader := tar.NewReader(reader)

	// Try to read the first file header.
	_, err := tarReader.Next()

	// If err is nil, at least one valid file header exists, so treat it as a valid TAR.
	// If err is io.EOF, it is an empty TAR package with only end markers; this is usually valid, depending on business requirements.
	// Any other error, such as "archive/tar: invalid tar header", means the TAR is invalid.
	if err == nil || err == io.EOF {
		return true
	}

	fmt.Printf("验证失败原因: %v\n", err)
	return false
}

// GenerateUniqueName generates a unique test name.
func GenerateUniqueName(prefix string) string {
	timestamp := time.Now().UnixNano() / 1000000
	return fmt.Sprintf("%s-%d", prefix, timestamp)
}

// BuildStringWithLength builds a string with the specified length.
func BuildStringWithLength(char string, length int) string {
	return strings.Repeat(char, length)
}

// DeleteTestKN deletes a test knowledge network.
func DeleteTestKN(client *testutil.HTTPClient, knID string, branch string, t *testing.T) {
	resp := client.DELETE("/api/bkn-backend/v1/knowledge-networks/" + knID + "?branch=" + branch)
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		t.Logf("删除知识网络可能失败: status=%d, kn_id=%s, branch=%s, body=%v", resp.StatusCode, knID, branch, resp.Body)
	}
}

// CreateTestObjectType creates a test object type.
func CreateTestObjectType(client *testutil.HTTPClient, knID string, t *testing.T) (otID string) {
	payload := map[string]any{
		"name":         GenerateUniqueName("test-ot"),
		"description":  "测试对象类型",
		"kn_id":        knID,
		"primary_keys": []string{"id"},
		"display_key":  "name",
		"data_properties": []map[string]any{
			{
				"name":         "id",
				"display_name": "ID",
				"type":         "string",
			},
			{
				"name":         "name",
				"display_name": "名称",
				"type":         "string",
			},
		},
	}

	resp := client.POST("/api/bkn-backend/v1/knowledge-networks/"+knID+"/object-types", payload)
	if resp.StatusCode != 201 {
		t.Fatalf("创建对象类型失败: status=%d, body=%v", resp.StatusCode, resp.Body)
	}

	otID = resp.Body["id"].(string)
	return
}

// CreateTestRelationType creates a test relation type.
func CreateTestRelationType(client *testutil.HTTPClient, knID string, sourceOTID, targetOTID string, t *testing.T) (rtID string) {
	payload := map[string]any{
		"name":                  GenerateUniqueName("test-rt"),
		"description":           "测试关系类型",
		"kn_id":                 knID,
		"source_object_type_id": sourceOTID,
		"target_object_type_id": targetOTID,
		"type":                  "direct",
	}

	resp := client.POST("/api/bkn-backend/v1/knowledge-networks/"+knID+"/relation-types", payload)
	if resp.StatusCode != 201 {
		t.Fatalf("创建关系类型失败: status=%d, body=%v", resp.StatusCode, resp.Body)
	}

	rtID = resp.Body["id"].(string)
	return
}

// CreateTestActionType creates a test action type.
func CreateTestActionType(client *testutil.HTTPClient, knID string, objectTypeID string, t *testing.T) (atID string) {
	payload := map[string]any{
		"name":           GenerateUniqueName("test-at"),
		"description":    "测试行动类型",
		"kn_id":          knID,
		"action_type":    "add",
		"object_type_id": objectTypeID,
	}

	resp := client.POST("/api/bkn-backend/v1/knowledge-networks/"+knID+"/action-types", payload)
	if resp.StatusCode != 201 {
		t.Fatalf("创建行动类型失败: status=%d, body=%v", resp.StatusCode, resp.Body)
	}

	atID = resp.Body["id"].(string)
	return
}

// BuildSimpleBKNTar builds a simple BKN tar package for import tests.
func BuildSimpleBKNTar(knID string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Create the network.bkn file.
	networkContent := fmt.Sprintf(`---
type: network
id: %s
name: %s
version: "1.0.0"
---

# Business Knowledge Network

## Object: test_object

### Properties

| Property | Display Name | Type | Constraint | Description |
|----------|-------------|------|------------|-------------|
| id | ID | string | | 主键 |
| name | 名称 | string | | 名称 |

### Keys

- **Primary Keys**: id
- **Display Key**: name
`, knID, knID)

	networkHeader := &tar.Header{
		Name: "network.bkn",
		Mode: 0644,
		Size: int64(len(networkContent)),
	}
	if err := tw.WriteHeader(networkHeader); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(networkContent)); err != nil {
		return nil, err
	}

	// Create the object_types/test_object.bkn file.
	objectContent := `---
type: object_type
id: test_object
name: 测试对象
network: test_network
version: "1.0.0"
---

# 测试对象

### Data Properties

| Property | Display Name | Type | Constraint | Description |
|----------|-------------|------|------------|-------------|
| id | ID | string | | 主键 |
| name | 名称 | string | | 名称 |

### Keys

- **Primary Keys**: id
- **Display Key**: name
`

	objectHeader := &tar.Header{
		Name: "object_types/test_object.bkn",
		Mode: 0644,
		Size: int64(len(objectContent)),
	}
	if err := tw.WriteHeader(objectHeader); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(objectContent)); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// BuildFullBKNTar builds a complete BKN tar package containing objects, relations, and actions.
func BuildFullBKNTar(knID string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Create the network.bkn file.
	networkContent := fmt.Sprintf(`---
type: network
id: %s
name: %s
version: "1.0.0"
---

# Business Knowledge Network
`, knID, knID)

	networkHeader := &tar.Header{
		Name: "network.bkn",
		Mode: 0644,
		Size: int64(len(networkContent)),
	}
	if err := tw.WriteHeader(networkHeader); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(networkContent)); err != nil {
		return nil, err
	}

	// Create the object_types/customer.bkn file.
	customerContent := `---
type: object_type
id: customer
name: 客户
network: ` + knID + `
version: "1.0.0"
---

# 客户

### Data Properties

| Property | Display Name | Type | Constraint | Description |
|----------|-------------|------|------------|-------------|
| id | ID | string | | 主键 |
| name | 客户名称 | string | | 客户名称 |
| email | 邮箱 | string | | 邮箱地址 |

### Keys

- **Primary Keys**: id
- **Display Key**: name
`

	customerHeader := &tar.Header{
		Name: "object_types/customer.bkn",
		Mode: 0644,
		Size: int64(len(customerContent)),
	}
	if err := tw.WriteHeader(customerHeader); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(customerContent)); err != nil {
		return nil, err
	}

	// Create the object_types/order.bkn file.
	orderContent := `---
type: object_type
id: order
name: 订单
network: ` + knID + `
version: "1.0.0"
---

# 订单

### Data Properties

| Property | Display Name | Type | Constraint | Description |
|----------|-------------|------|------------|-------------|
| id | ID | string | | 主键 |
| order_no | 订单号 | string | | 订单号 |
| amount | 金额 | decimal | | 订单金额 |

### Keys

- **Primary Keys**: id
- **Display Key**: order_no
`

	orderHeader := &tar.Header{
		Name: "object_types/order.bkn",
		Mode: 0644,
		Size: int64(len(orderContent)),
	}
	if err := tw.WriteHeader(orderHeader); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(orderContent)); err != nil {
		return nil, err
	}

	// Create the relation_types/customer_order.bkn file.
	relationContent := `---
type: relation_type
id: customer_order
name: 客户订单
version: "1.0.0"
---

# 客户订单关系

### Endpoints

| Source | Target | Type | Required | Min | Max |
|--------|--------|------|----------|-----|-----|
| customer | order | direct | true | 0 | N |

### Mapping Rules

| Source Property | Target Property |
|-----------------|-----------------|
| id | customer_id |
`

	relationHeader := &tar.Header{
		Name: "relation_types/customer_order.bkn",
		Mode: 0644,
		Size: int64(len(relationContent)),
	}
	if err := tw.WriteHeader(relationHeader); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(relationContent)); err != nil {
		return nil, err
	}

	// Create the action_types/create_order.bkn file.
	actionContent := `---
type: action_type
id: create_order
name: 创建订单
action_type: add
version: "1.0.0"
---

# 创建订单行动

### Bound Object

- **Object**: order

### Tool Configuration

| Type | Tool ID |
|------|---------|
| tool | create_order_tool |
`

	actionHeader := &tar.Header{
		Name: "action_types/create_order.bkn",
		Mode: 0644,
		Size: int64(len(actionContent)),
	}
	if err := tw.WriteHeader(actionHeader); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(actionContent)); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// BuildTarWithoutNetworkBKN builds a tar package without network.bkn for negative tests.
func BuildTarWithoutNetworkBKN() ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Include only object_types, without network.bkn.
	objectContent := `---
type: object_type
id: orphan_object
name: 孤立对象
version: "1.0.0"
---

# 孤立对象（缺少 network.bkn）

### Data Properties

| Property | Display Name | Type | Constraint | Description |
|----------|-------------|------|------------|-------------|
| id | ID | string | | 主键 |

### Keys

- **Primary Keys**: id
- **Display Key**: id
`

	header := &tar.Header{
		Name: "object_types/orphan_object.bkn",
		Mode: 0644,
		Size: int64(len(objectContent)),
	}
	if err := tw.WriteHeader(header); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(objectContent)); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// CleanupKNs cleans up test knowledge networks.
func CleanupKNs(client *testutil.HTTPClient, t *testing.T) {
	// Query all test knowledge networks.
	resp := client.GET("/api/bkn-backend/v1/knowledge-networks?offset=0&limit=100")
	if resp.StatusCode != 200 {
		t.Logf("查询知识网络列表失败: status=%d", resp.StatusCode)
		return
	}

	entries, ok := resp.Body["entries"].([]any)
	if !ok {
		return
	}

	for _, entry := range entries {
		kn, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		knID, _ := kn["id"].(string)
		branch, _ := kn["branch"].(string)

		DeleteTestKN(client, knID, branch, t)

	}
}

// BuildTarFromExamplesDir builds a tar package from the examples directory.
// exampleName: example directory name, such as "k8s-network".
func BuildTarFromExamplesDir(exampleName string) ([]byte, error) {
	examplesDir := filepath.Join("helpers", "examples", exampleName)
	return buildTarFromDir(examplesDir)
}

// buildTarFromDir builds a tar package from the specified directory.
func buildTarFromDir(dirPath string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := filepath.Walk(dirPath, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories.
		if fi.IsDir() {
			return nil
		}

		// Calculate the relative path from dirPath.
		relPath, err := filepath.Rel(dirPath, file)
		if err != nil {
			return err
		}

		// Use forward slashes as path separators inside the tar package.
		relPath = filepath.ToSlash(relPath)

		// Create the tar header.
		header := &tar.Header{
			Name: relPath,
			Mode: int64(fi.Mode()),
			Size: fi.Size(),
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// Open the file and write it to the tar package.
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()

		if _, err := io.Copy(tw, f); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// GetExampleNames gets the list of available example names.
func GetExampleNames() []string {
	return []string{"k8s-network"}
}

// VerifyObjectTypesExist verifies that object types exist.
func VerifyObjectTypesExist(client *testutil.HTTPClient, knID string, t *testing.T) []any {
	resp := client.GET("/api/bkn-backend/v1/knowledge-networks/" + knID + "/object-types?offset=0&limit=100")
	if resp.StatusCode != 200 {
		t.Fatalf("查询对象类型失败: status=%d, body=%v", resp.StatusCode, resp.Body)
	}
	entries, ok := resp.Body["entries"].([]any)
	if !ok {
		t.Fatalf("查询对象类型返回格式错误: body=%v", resp.Body)
	}
	return entries
}

// VerifyRelationTypesExist verifies that relation types exist.
func VerifyRelationTypesExist(client *testutil.HTTPClient, knID string, t *testing.T) []any {
	resp := client.GET("/api/bkn-backend/v1/knowledge-networks/" + knID + "/relation-types?offset=0&limit=100")
	if resp.StatusCode != 200 {
		t.Fatalf("查询关系类型失败: status=%d, body=%v", resp.StatusCode, resp.Body)
	}
	entries, ok := resp.Body["entries"].([]any)
	if !ok {
		t.Fatalf("查询关系类型返回格式错误: body=%v", resp.Body)
	}
	return entries
}

// VerifyActionTypesExist verifies that action types exist.
func VerifyActionTypesExist(client *testutil.HTTPClient, knID string, t *testing.T) []any {
	resp := client.GET("/api/bkn-backend/v1/knowledge-networks/" + knID + "/action-types?offset=0&limit=100")
	if resp.StatusCode != 200 {
		t.Fatalf("查询行动类型失败: status=%d, body=%v", resp.StatusCode, resp.Body)
	}
	entries, ok := resp.Body["entries"].([]any)
	if !ok {
		t.Fatalf("查询行动类型返回格式错误: body=%v", resp.Body)
	}
	return entries
}

// VerifyConceptGroupsExist verifies that concept groups exist.
func VerifyConceptGroupsExist(client *testutil.HTTPClient, knID string, t *testing.T) []any {
	resp := client.GET("/api/bkn-backend/v1/knowledge-networks/" + knID + "/concept-groups?offset=0&limit=100")
	if resp.StatusCode != 200 {
		t.Fatalf("查询概念分组失败: status=%d, body=%v", resp.StatusCode, resp.Body)
	}
	entries, ok := resp.Body["entries"].([]any)
	if !ok {
		t.Fatalf("查询概念分组返回格式错误: body=%v", resp.Body)
	}
	return entries
}

// VerifyMetricsCountAtLeast verifies that GET .../metrics returns at least minCount entries for BKN tar import acceptance with metrics.
func VerifyMetricsCountAtLeast(client *testutil.HTTPClient, knID string, t *testing.T, minCount int) int {
	resp := client.GET("/api/bkn-backend/v1/knowledge-networks/" + knID + "/metrics?offset=0&limit=500")
	if resp.StatusCode != 200 {
		t.Fatalf("查询指标失败: status=%d body=%v", resp.StatusCode, resp.Body)
	}
	entries, ok := resp.Body["entries"].([]any)
	if !ok {
		t.Fatalf("查询指标返回格式错误: body=%v", resp.Body)
	}
	total := len(entries)
	if tc, ok := resp.Body["total_count"]; ok {
		switch v := tc.(type) {
		case float64:
			total = int(v)
		case int:
			total = v
		case int64:
			total = int(v)
		}
	}
	if len(entries) < minCount && total < minCount {
		t.Fatalf("指标数量不足: want>=%d got_entries=%d total_count=%d", minCount, len(entries), total)
	}
	return len(entries)
}
