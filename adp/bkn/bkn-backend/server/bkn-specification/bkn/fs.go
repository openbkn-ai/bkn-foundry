// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// WalkFunc is the callback for FileSystem.Walk.
type WalkFunc func(path string, info fs.FileInfo, err error) error

// FileSystem defines an abstract file-system interface.
type FileSystem interface {
	// ReadFile reads file contents.
	ReadFile(path string) ([]byte, error)
	// Stat gets file information.
	Stat(path string) (fs.FileInfo, error)
	// ReadDir reads directory contents.
	ReadDir(path string) ([]fs.DirEntry, error)
	// IsDir reports whether a path is a directory.
	IsDir(path string) bool
	// Abs gets an absolute path.
	Abs(path string) string
	// Join joins path components.
	Join(elem ...string) string
	// Dir gets a directory path.
	Dir(path string) string
	// Base gets a file name.
	Base(path string) string
	// Ext gets a file extension.
	Ext(path string) string
	// Walk recursively traverses a directory.
	Walk(root string, fn WalkFunc) error
	// Rel returns a relative path.
	Rel(basepath, targpath string) (string, error)
	// WriteFile writes a file.
	WriteFile(path string, data []byte, perm fs.FileMode) error
}

// OSFileSystem implements FileSystem using the operating system file system.
type OSFileSystem struct{}

func NewOSFileSystem() FileSystem {
	return &OSFileSystem{}
}

func (fs *OSFileSystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (fs *OSFileSystem) Stat(path string) (fs.FileInfo, error) {
	return os.Stat(path)
}

func (fs *OSFileSystem) ReadDir(path string) ([]fs.DirEntry, error) {
	return os.ReadDir(path)
}

func (fs *OSFileSystem) IsDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func (fs *OSFileSystem) Abs(path string) string {
	abs, _ := filepath.Abs(path)
	return abs
}

func (fs *OSFileSystem) Join(elem ...string) string {
	return filepath.Join(elem...)
}

func (fs *OSFileSystem) Dir(path string) string {
	return filepath.Dir(path)
}

func (fs *OSFileSystem) Base(path string) string {
	return filepath.Base(path)
}

func (fs *OSFileSystem) Ext(path string) string {
	return strings.ToLower(filepath.Ext(path))
}

func (f *OSFileSystem) Walk(root string, fn WalkFunc) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		return fn(path, info, err)
	})
}

func (f *OSFileSystem) Rel(basepath, targpath string) (string, error) {
	return filepath.Rel(basepath, targpath)
}

func (f *OSFileSystem) WriteFile(path string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(path, data, perm)
}

// MemoryFileSystem implements an in-memory file system.
type MemoryFileSystem struct {
	files map[string][]byte // File path to content.
}

func NewMemoryFileSystem() *MemoryFileSystem {
	return &MemoryFileSystem{
		files: make(map[string][]byte),
	}
}

// AddFile adds a file to the in-memory file system.
func (mfs *MemoryFileSystem) AddFile(path string, content []byte) {
	mfs.files[path] = content
}

// AddFiles adds files in batches.
func (mfs *MemoryFileSystem) AddFiles(files map[string][]byte) {
	for path, content := range files {
		mfs.files[path] = content
	}
}

func (mfs *MemoryFileSystem) ReadFile(path string) ([]byte, error) {
	if content, ok := mfs.files[path]; ok {
		return content, nil
	}
	return nil, fmt.Errorf("file not found: %s", path)
}

func (mfs *MemoryFileSystem) Stat(path string) (fs.FileInfo, error) {
	if _, ok := mfs.files[path]; ok {
		return &memoryFileInfo{name: path, size: int64(len(mfs.files[path]))}, nil
	}
	// "." or "" represents the root directory, which exists when any file is present.
	if (path == "." || path == "") && len(mfs.files) > 0 {
		return &memoryFileInfo{name: path, isDir: true}, nil
	}
	// Check whether this is a directory by looking for child files.
	for filePath := range mfs.files {
		if strings.HasPrefix(filePath, path+"/") || strings.HasPrefix(filePath, path+"\\") {
			return &memoryFileInfo{name: path, isDir: true}, nil
		}
	}
	return nil, fmt.Errorf("path not found: %s", path)
}

func (mfs *MemoryFileSystem) ReadDir(path string) ([]fs.DirEntry, error) {
	entries := make(map[string]*memoryDirEntry)
	prefix := path + "/"
	if path == "." || path == "" {
		prefix = ""
	}

	for filePath := range mfs.files {
		if !strings.HasPrefix(filePath, prefix) {
			continue
		}
		relPath := strings.TrimPrefix(filePath, prefix)
		parts := strings.Split(relPath, "/")
		if len(parts) == 0 {
			continue
		}
		name := parts[0]
		if _, exists := entries[name]; !exists {
			isDir := len(parts) > 1
			entries[name] = &memoryDirEntry{
				name:  name,
				isDir: isDir,
			}
		}
	}

	result := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	return result, nil
}

func (mfs *MemoryFileSystem) IsDir(path string) bool {
	// "." or "" represents the root directory.
	if (path == "." || path == "") && len(mfs.files) > 0 {
		return true
	}
	// Check whether child files exist.
	for filePath := range mfs.files {
		if strings.HasPrefix(filePath, path+"/") || strings.HasPrefix(filePath, path+"\\") {
			return true
		}
	}
	return false
}

func (mfs *MemoryFileSystem) Abs(path string) string {
	// The in-memory file system uses normalized paths.
	return filepath.Clean(path)
}

func (mfs *MemoryFileSystem) Join(elem ...string) string {
	return filepath.Join(elem...)
}

func (mfs *MemoryFileSystem) Dir(path string) string {
	return filepath.Dir(path)
}

func (mfs *MemoryFileSystem) Base(path string) string {
	return filepath.Base(path)
}

func (mfs *MemoryFileSystem) Ext(path string) string {
	return strings.ToLower(filepath.Ext(path))
}

func (mfs *MemoryFileSystem) Walk(root string, fn WalkFunc) error {
	prefix := root + "/"
	if root == "." || root == "" {
		prefix = ""
	}

	// Collect and sort paths for deterministic walk order
	var paths []string
	for p := range mfs.files {
		if strings.HasPrefix(p, prefix) || p == root {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	// Walk root directory itself
	rootInfo, err := mfs.Stat(root)
	if err != nil {
		return err
	}
	if err := fn(root, rootInfo, nil); err != nil {
		return err
	}

	for _, p := range paths {
		if p == root {
			continue
		}
		info, err := mfs.Stat(p)
		if fnErr := fn(p, info, err); fnErr != nil {
			return fnErr
		}
	}
	return nil
}

func (mfs *MemoryFileSystem) Rel(basepath, targpath string) (string, error) {
	return filepath.Rel(basepath, targpath)
}

func (mfs *MemoryFileSystem) WriteFile(path string, data []byte, perm fs.FileMode) error {
	mfs.files[path] = data
	return nil
}

// memoryFileInfo contains in-memory file information.
type memoryFileInfo struct {
	name  string
	size  int64
	isDir bool
}

func (fi *memoryFileInfo) Name() string       { return fi.name }
func (fi *memoryFileInfo) Size() int64        { return fi.size }
func (fi *memoryFileInfo) Mode() fs.FileMode  { return 0644 }
func (fi *memoryFileInfo) ModTime() time.Time { return time.Time{} }
func (fi *memoryFileInfo) IsDir() bool        { return fi.isDir }
func (fi *memoryFileInfo) Sys() interface{}   { return nil }

// memoryDirEntry represents an in-memory directory entry.
type memoryDirEntry struct {
	name  string
	isDir bool
}

func (de *memoryDirEntry) Name() string      { return de.name }
func (de *memoryDirEntry) IsDir() bool       { return de.isDir }
func (de *memoryDirEntry) Type() fs.FileMode { return 0644 }
func (de *memoryDirEntry) Info() (fs.FileInfo, error) {
	return &memoryFileInfo{name: de.name, isDir: de.isDir}, nil
}
