package logic

import "encoding/json"

func applyCapabilityName(openapiSpec, name string) string {
	trimmed := name
	if trimmed == "" {
		return openapiSpec
	}

	var document map[string]interface{}
	if err := json.Unmarshal([]byte(openapiSpec), &document); err != nil {
		return openapiSpec
	}

	info, ok := document["info"].(map[string]interface{})
	if !ok {
		info = map[string]interface{}{}
		document["info"] = info
	}

	info["title"] = trimmed

	patched, err := json.Marshal(document)
	if err != nil {
		return openapiSpec
	}

	return string(patched)
}
