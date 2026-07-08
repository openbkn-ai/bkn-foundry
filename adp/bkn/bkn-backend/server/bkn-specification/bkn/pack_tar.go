// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// PackDirToTar packs a BKN directory into a tar archive using the system tar command.
//
// On macOS, sets COPYFILE_DISABLE=1 to prevent AppleDouble (._*.bkn) extended-attribute
// files. Without this, LoadNetworkFromTar would treat ._*.bkn as valid BKN files,
// producing empty ObjectTypes and validation errors like "对象类名称为空".
//
// sourceDir: Path to the BKN network directory (e.g. examples/k8s-network).
// outputPath: Path for the output .tar (or .tar.gz if gzip is true).
func PackDirToTar(sourceDir, outputPath string, gzip bool) error {
	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		return fmt.Errorf("resolve source dir: %w", err)
	}
	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}

	info, err := os.Stat(absSource)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source directory not found: %s", absSource)
		}
		return fmt.Errorf("stat source: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source is not a directory: %s", absSource)
	}

	args := []string{"-cf", absOutput, "."}
	if gzip {
		args = []string{"-czf", absOutput, "."}
	}

	cmd := exec.Command("tar", args...)
	cmd.Dir = absSource
	cmd.Env = os.Environ()
	if runtime.GOOS == "darwin" {
		cmd.Env = append(cmd.Env, "COPYFILE_DISABLE=1")
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tar failed (exit %v): %s", err, string(out))
	}
	return nil
}
