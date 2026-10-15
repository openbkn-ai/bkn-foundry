// Copyright openbkn.ai

package bkn

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Object metric archive files carry the complete V1 record as JSON. The BKN
// parser deliberately does not depend on backend interfaces: interfaces use
// this package for network diff and capability declarations.
func objectMetricArchiveIdentity(data []byte) (string, int, error) {
	var envelope struct {
		Definition struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"definition"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", 0, fmt.Errorf("invalid object metric JSON: %w", err)
	}
	id, version := envelope.Definition.ID, envelope.Definition.Version
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\\`) {
		return "", 0, fmt.Errorf("invalid object metric id %q", id)
	}
	for _, char := range id {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '-' && char != '_' && char != '.' {
			return "", 0, fmt.Errorf("invalid object metric id %q", id)
		}
	}
	if version < 1 {
		return "", 0, fmt.Errorf("invalid object metric version %d", version)
	}
	return id, version, nil
}

func objectMetricArchiveFileName(id string, version int) string {
	return id + "@" + strconv.Itoa(version) + ".json"
}

func objectMetricArchiveMetadata(data []byte) (id string, version int, name, description string) {
	var envelope struct {
		Definition struct {
			ID          string `json:"id"`
			Version     int    `json:"version"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"definition"`
	}
	_ = json.Unmarshal(data, &envelope)
	return envelope.Definition.ID, envelope.Definition.Version,
		envelope.Definition.Name, envelope.Definition.Description
}
