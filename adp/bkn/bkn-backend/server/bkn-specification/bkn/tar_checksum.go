// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"io"
	"path/filepath"
)

// ComputeChecksumFromTar 从 tar 流计算所有定义的 checksum。
// 返回 map["type:id"] = "sha256:hash"。
func ComputeChecksumFromTar(r io.Reader) (map[string]string, error) {
	mfs, rootFile, err := ExtractTarToMemory(r)
	if err != nil {
		return nil, fmt.Errorf("failed to extract tar: %w", err)
	}
	rootDir := filepath.Dir(rootFile)
	return ComputeNetworkChecksums(mfs, rootDir)
}

// GenerateChecksumFromTar 从 tar 流生成 CHECKSUM 文件内容。
// 返回 CHECKSUM 文件的字符串内容。
func GenerateChecksumFromTar(r io.Reader) (string, error) {
	mfs, rootFile, err := ExtractTarToMemory(r)
	if err != nil {
		return "", fmt.Errorf("failed to extract tar: %w", err)
	}
	rootDir := filepath.Dir(rootFile)
	return GenerateChecksumFileWithFS(mfs, rootDir)
}

// VerifyChecksumFromTar 验证 tar 流内的 CHECKSUM 文件是否与实际内容一致。
// tar 包中必须包含 CHECKSUM 文件。
func VerifyChecksumFromTar(r io.Reader) (bool, []string) {
	mfs, rootFile, err := ExtractTarToMemory(r)
	if err != nil {
		return false, []string{fmt.Sprintf("failed to extract tar: %v", err)}
	}
	rootDir := filepath.Dir(rootFile)
	return VerifyChecksumFileWithFS(mfs, rootDir)
}

// DiffNetworksFromTar 比较两个 tar 包之间的定义差异。
// 返回 DiffResult，包含 create/update/skip/delete 条目。
func DiffNetworksFromTar(oldTar, newTar io.Reader) (*DiffResult, error) {
	oldChecksums, err := ComputeChecksumFromTar(oldTar)
	if err != nil {
		return nil, fmt.Errorf("failed to compute old checksums: %w", err)
	}
	newChecksums, err := ComputeChecksumFromTar(newTar)
	if err != nil {
		return nil, fmt.Errorf("failed to compute new checksums: %w", err)
	}
	return DiffNetworks(oldChecksums, newChecksums), nil
}
