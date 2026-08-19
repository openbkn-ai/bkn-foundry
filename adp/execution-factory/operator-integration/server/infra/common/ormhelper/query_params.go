package ormhelper

import "strings"

// SortOrder sort direction enumeration.
type SortOrder string

const (
	SortOrderAsc  SortOrder = "ASC"
	SortOrderDesc SortOrder = "DESC"
)

// ToUpper converts the sort direction to an uppercase string.
func (s SortOrder) ToUpper() SortOrder {
	return SortOrder(strings.ToUpper(string(s)))
}

// String implements the Stringer interface.
func (s SortOrder) String() string {
	return string(s)
}

// IsValid verifies whether the sorting direction is valid.
func (s SortOrder) IsValid() bool {
	return s == SortOrderAsc || s == SortOrderDesc
}

// PaginationParams Pagination parameters.
type PaginationParams struct {
	Page     int `json:"page" validate:"min=1"`              // Page number, starting from 1.
	PageSize int `json:"page_size" validate:"min=1,max=100"` // Quantity per page.
}

// SortField sort field.
type SortField struct {
	Field string    `json:"field"` // Database field name (the caller is responsible for passing in the correct field name)
	Order SortOrder `json:"order"` // Sorting direction.
}

// SortParams sorting parameters.
type SortParams struct {
	Fields []SortField `json:"fields,omitempty"` // Support multi-field sorting.
}

// CursorParams cursor parameters.
type CursorParams struct {
	Field     string    `json:"field,omitempty"`     // Cursor field name (the caller is responsible for passing in the correct field name)
	Value     any       `json:"value,omitempty"`     // cursor value.
	Direction SortOrder `json:"direction,omitempty"` // Cursor direction, default ASC.
}

// QueryResult general query results.
type QueryResult struct {
	Total      int64 `json:"total"`       // Total number of records.
	Page       int   `json:"page"`        // Current page number.
	PageSize   int   `json:"page_size"`   // Quantity per page.
	TotalPages int   `json:"total_pages"` // Total pages.
	HasNext    bool  `json:"has_next"`    // Is there a next page?.
	HasPrev    bool  `json:"has_prev"`    // Is there a previous page?.
}

// CalculateQueryResult calculates the paging information of query results.
func CalculateQueryResult(total int64, pagination *PaginationParams) *QueryResult {
	if pagination == nil || pagination.Page <= 0 || pagination.PageSize <= 0 {
		return &QueryResult{
			Total:      total,
			Page:       1,
			PageSize:   int(total),
			TotalPages: 1,
			HasNext:    false,
			HasPrev:    false,
		}
	}

	totalPages := int((total + int64(pagination.PageSize) - 1) / int64(pagination.PageSize))

	return &QueryResult{
		Total:      total,
		Page:       pagination.Page,
		PageSize:   pagination.PageSize,
		TotalPages: totalPages,
		HasNext:    pagination.Page < totalPages,
		HasPrev:    pagination.Page > 1,
	}
}
