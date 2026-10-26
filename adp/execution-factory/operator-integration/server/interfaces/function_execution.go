// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

const (
	FunctionVersionHeader  = "X-BKN-Function-Version"
	FunctionCodeHashHeader = "X-BKN-Function-Code-SHA256"
)

// FunctionExecution identifies the code actually sent to this deployment's
// Function runtime. A metadata version may be edited; the code hash is separate.
type FunctionExecution struct {
	ToolboxID       string `json:"toolbox_id"`
	ToolID          string `json:"tool_id"`
	FunctionVersion string `json:"function_version"`
	CodeSHA256      string `json:"code_sha256"`
}
