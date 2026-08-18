// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var hanTextPattern = regexp.MustCompile(`\p{Han}`)

func TestProductionGoCodeHasNoHardCodedChineseText(t *testing.T) {
	var matches []string
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}

		scanner := bufio.NewScanner(file)
		for lineNumber := 1; scanner.Scan(); lineNumber++ {
			if hanTextPattern.MatchString(scanner.Text()) {
				matches = append(matches, fmt.Sprintf("%s:%d", path, lineNumber))
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil {
			return scanErr
		}
		return closeErr
	})
	if err != nil {
		t.Fatalf("scan production Go code: %v", err)
	}
	if len(matches) > 0 {
		t.Fatalf("production Go code contains hard-coded Chinese text:\n%s", strings.Join(matches, "\n"))
	}
}
