// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

const (
	// MaxDatasetBulkDocuments 限制单次请求处理的文档条数。
	MaxDatasetBulkDocuments = 1000

	// MaxDatasetBulkBodyBytes 限制批量写入的原始请求体大小。
	MaxDatasetBulkBodyBytes = 32 * 1024 * 1024

	DocumentInvalid        = "invalid_document"
	DocumentConflict       = "version_conflict"
	DocumentUnavailable    = "write_unavailable"
	DocumentRejected       = "write_rejected"
	DocumentOutcomeUnknown = "outcome_unknown"
)

// DeleteByQueryResult 仅包含可向调用方公开的删除进度。
type DeleteByQueryResult struct {
	Deleted          int64                 `json:"deleted"`
	VersionConflicts int64                 `json:"version_conflicts"`
	TimedOut         bool                  `json:"timed_out"`
	Failures         []DocumentWriteResult `json:"failures"`
}

// DocumentWrite 是 materialize 后传递给索引层的完整文档及目标 ID；Document 不含 _id。
type DocumentWrite struct {
	ID       string         `json:"id"`
	Document map[string]any `json:"document"`
}

// DocumentWriteResult 与输入顺序一致，不携带源端错误细节。
type DocumentWriteResult struct {
	ID        string `json:"id,omitempty"`
	Status    int    `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
	Retryable bool   `json:"retryable"`
}
