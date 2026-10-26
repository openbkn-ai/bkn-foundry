// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"encoding/hex"
	neturl "net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

func platformFunctionExecution(req *interfaces.ExecuteToolReq, rawURL string, resp *interfaces.HTTPResponse) *interfaces.FunctionExecution {
	if req == nil || resp == nil || !isPlatformFunctionTarget(rawURL) {
		return nil
	}
	target, err := neturl.Parse(rawURL)
	if err != nil || target.User != nil || target.Fragment != "" {
		return nil
	}
	prefix := strings.TrimSuffix(interfaces.GetAOIFuncExecPath(), ":version")
	version := strings.TrimPrefix(target.Path, prefix)
	if uuid.Validate(version) != nil {
		return nil
	}
	if responseHeader(resp.Headers, interfaces.FunctionVersionHeader) != version {
		return nil
	}
	digest := responseHeader(resp.Headers, interfaces.FunctionCodeHashHeader)
	decoded, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	if err != nil || len(decoded) != 32 || digest != "sha256:"+hex.EncodeToString(decoded) {
		return nil
	}
	return &interfaces.FunctionExecution{
		ToolboxID: req.BoxID, ToolID: req.ToolID, FunctionVersion: version, CodeSHA256: digest,
	}
}

func responseHeader(headers map[string]any, name string) string {
	var result string
	seen := false
	for key, value := range headers {
		if !strings.EqualFold(key, name) {
			continue
		}
		text, ok := value.(string)
		// Duplicate spellings must not produce a map-iteration-dependent proof.
		if !ok || seen {
			return ""
		}
		seen = true
		result = text
	}
	return result
}
