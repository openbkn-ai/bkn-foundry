package interfaces

import "context"

// BizCategory Business Category.
//
//go:generate mockgen -source=logics_category.go -destination=../mocks/category.go -package=mocks
type BizCategory string

func (c BizCategory) String() string {
	return string(c)
}

const (
	CategoryTypeOther  = BizCategory("other_category") // Other categories.
	CategoryTypeSystem = BizCategory("system")         // System built-in classification.
)

// CategoryInfo category information.
type CategoryInfo struct {
	CategoryType BizCategory `json:"category_type"`
	CategoryName string      `json:"name"` // (Supports internationalization)
}

// CreateCategoryReq adds a new category request.
type CreateCategoryReq struct {
	UserID       string      `header:"user_id"`
	CategoryType BizCategory `json:"category_type"`
	CategoryName string      `json:"name" validate:"required"`
}

// CreateCategoryResp adds category response.
type CreateCategoryResp struct {
	CategoryType BizCategory `json:"category_type"`
	CategoryName string      `json:"name"`
}

// UpdateCategoryReq update category request.
type UpdateCategoryReq struct {
	UserID       string      `header:"user_id"`
	CategoryType BizCategory `uri:"category_type" validate:"required"`
	CategoryName string      `json:"name" validate:"required"`
}

// UpdateCategoryResp Update category response.
type UpdateCategoryResp struct {
	CategoryType BizCategory `json:"category_type"`
	CategoryName string      `json:"name"`
}

// DeleteCategoryReq delete category request.
type DeleteCategoryReq struct {
	UserID       string      `header:"user_id"`
	CategoryType BizCategory `uri:"category_type" validate:"required"`
}

// DeleteCategoryResp Delete category response.
type DeleteCategoryResp struct {
	CategoryType BizCategory `json:"category_type"`
}

// CategoryManager manages categories.
type CategoryManager interface {
	// Get category list.
	GetCategoryList(ctx context.Context) (categoryList []*CategoryInfo, err error)
	// Check if the category exists.
	CheckCategory(category BizCategory) (isExist bool)
	// Get category name.
	GetCategoryName(ctx context.Context, category BizCategory) (categoryName string)
	// Update category.
	UpdateCategory(ctx context.Context, req *UpdateCategoryReq) (resp *UpdateCategoryResp, err error)
	// Add new category.
	CreateCategory(ctx context.Context, req *CreateCategoryReq) (resp *CreateCategoryResp, err error)
	// Delete category.
	DeleteCategory(ctx context.Context, req *DeleteCategoryReq) (err error)
	// Add categories in batches.
	BatchCreateCategory(ctx context.Context, req []*CreateCategoryReq) (resp []*CreateCategoryResp, err error)
}
