// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"archive/tar"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// LoadNetworkFromTar 从 tar 包直接加载 BKN 网络
// 无需写入本地文件系统，完全在内存中处理
func LoadNetworkFromTar(tarReader io.Reader) (*BknNetwork, error) {
	// 1. 解压 tar 包到内存文件系统
	mfs, rootDir, err := ExtractTarToMemory(tarReader)
	if err != nil {
		return nil, fmt.Errorf("failed to extract tar: %w", err)
	}

	// 2. 使用内存文件系统加载网络（使用目录路径）
	return LoadNetworkWithFS(mfs, rootDir)
}

// ExtractTarToMemory 将 tar 包解压到内存文件系统
// 返回内存文件系统和根目录路径
func ExtractTarToMemory(reader io.Reader) (*MemoryFileSystem, string, error) {
	mfs := NewMemoryFileSystem()
	tr := tar.NewReader(reader)

	var rootDir string

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("failed to read tar header: %w", err)
		}

		// 跳过目录
		if header.Typeflag == tar.TypeDir {
			continue
		}

		base := filepath.Base(header.Name)
		// 跳过 macOS AppleDouble 扩展属性文件（._*），避免解析出空 ObjectType
		if strings.HasPrefix(base, "._") {
			if _, err := io.CopyN(io.Discard, tr, header.Size); err != nil {
				return nil, "", fmt.Errorf("failed to skip %s body: %w", header.Name, err)
			}
			continue
		}

		// 只处理支持的文件类型（.bkn, .md）以及 CHECKSUM 文件
		ext := strings.ToLower(filepath.Ext(header.Name))
		if !SupportedExtensions[ext] && base != ChecksumFileName {
			if _, err := io.CopyN(io.Discard, tr, header.Size); err != nil {
				return nil, "", fmt.Errorf("failed to skip %s body: %w", header.Name, err)
			}
			continue
		}

		// 读取文件内容
		content := make([]byte, header.Size)
		if _, err := io.ReadFull(tr, content); err != nil {
			return nil, "", fmt.Errorf("failed to read file %s: %w", header.Name, err)
		}

		// 标准化路径：去除 leading "./"，统一使用 / 分隔符
		path := strings.TrimPrefix(filepath.ToSlash(header.Name), "./")
		mfs.AddFile(path, content)

		// 检查是否是根文件候选，记录其目录
		if strings.EqualFold(base, RootFileName) {
			rootDir = filepath.Dir(path)
			if rootDir == "" {
				rootDir = "."
			}
		}
	}

	if rootDir == "" {
		return nil, "", fmt.Errorf("no root network file found in tar")
	}

	return mfs, rootDir, nil
}
