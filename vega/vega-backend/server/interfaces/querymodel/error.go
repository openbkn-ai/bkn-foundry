// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

type ErrorKind string

const (
	ErrorSyntax      ErrorKind = "syntax"
	ErrorUnsupported ErrorKind = "unsupported_syntax"
	ErrorLimit       ErrorKind = "input_limit"
	ErrorInvalidTree ErrorKind = "invalid_tree"
)

// CompileError 仅包含可公开的分类和位置，不包含 SQL 原文或目标信息。
type CompileError struct {
	Kind    ErrorKind
	Origin  Origin
	Message string
}

func (e *CompileError) Error() string {
	return string(e.Kind) + ": " + e.Message
}
