// Package category implements the interfaces.CategoryManager interface.
package category

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/localize"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// requireOperatorTypePermission verifies that the caller holds the specified operation permission on the operator type.
//
// The classification is a global classification method and does not belong to any single operator. There is no resource ID to judge, so it is based on type level (ResourceIDAll)
// Determination, the semantics is the same as CheckCreatePermission in logics/auth/decision.go.
//
// It only takes effect on the public side: the internal side (internal-v1) is called between services, and the built-in classification injection during startup also takes this path.
// (see driveradapters/category/init_data.go), following the existing idiom within the service to skip the determination.
// The read interface (GetCategoryList) does not judge: the taxonomy is just a name dictionary and does not contain tenant data. Tightening will interrupt the non-super management front end.
func (c *categoryManager) requireOperatorTypePermission(ctx context.Context, userID string,
	operation interfaces.AuthOperationType) error {
	if !common.IsPublicAPIFromCtx(ctx) {
		return nil
	}
	accessor, err := c.AuthService.GetAccessor(ctx, userID)
	if err != nil {
		return err
	}
	authorized, err := c.AuthService.OperationCheckAll(ctx, accessor,
		interfaces.ResourceIDAll, interfaces.AuthResourceTypeOperator, operation)
	if err != nil {
		return err
	}
	if !authorized {
		return errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden, nil)
	}
	return nil
}

// GetCategoryName Gets the category name.
func (c *categoryManager) GetCategoryName(ctx context.Context, category interfaces.BizCategory) (categoryName string) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, nil)
	if category == "" {
		return
	}
	switch category {
	case interfaces.CategoryTypeOther:
		return c.getCategoryOther(ctx).CategoryName
	case interfaces.CategoryTypeSystem:
		return c.getCategorySystem(ctx).CategoryName
	default:
		// Get category name from cache.
		value, ok := c.Cache.Get(category.String())
		if ok {
			categoryName = value.(string)
			return
		}
		var categoryDB *model.CategoryDB
		categoryDB, err := c.DBCategory.SelectListByCategoryID(ctx, nil, category.String())
		if err != nil {
			c.logger.Errorf("get category name failed, err: %v", err)
			return ""
		}
		if categoryDB == nil {
			return ""
		}
		categoryName = categoryDB.CategoryName
		// Store category names in cache.
		c.Cache.Set(category.String(), categoryName)
		return
	}
}

// CheckCategory checks whether the category exists.
func (c *categoryManager) CheckCategory(category interfaces.BizCategory) (isExist bool) {
	if category == interfaces.CategoryTypeOther || category == interfaces.CategoryTypeSystem {
		isExist = true
		return
	}
	var categoryDB *model.CategoryDB
	categoryDB, err := c.DBCategory.SelectListByCategoryID(context.Background(), nil, category.String())
	if err != nil {
		c.logger.Errorf("check category failed, err: %v", err)
		return false
	}
	isExist = categoryDB != nil
	return
}

// GetCategoryList
func (c *categoryManager) GetCategoryList(ctx context.Context) (categoryList []*interfaces.CategoryInfo, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	categoryDBList, err := c.DBCategory.SelectList(ctx, nil)
	if err != nil {
		return
	}

	// The built-in "Other" category is added by default.
	categoryList = append(categoryList, c.getCategoryOther(ctx), c.getCategorySystem(ctx))

	for _, categoryDB := range categoryDBList {
		categoryList = append(categoryList, &interfaces.CategoryInfo{
			CategoryType: interfaces.BizCategory(categoryDB.CategoryID),
			CategoryName: categoryDB.CategoryName,
		})
	}
	return
}

func (c *categoryManager) getCategoryOther(ctx context.Context) *interfaces.CategoryInfo {
	language := common.GetLanguageByCtx(ctx)
	tr := localize.NewI18nTranslator(language)
	categoryName := tr.Trans("category." + interfaces.CategoryTypeOther.String())
	return &interfaces.CategoryInfo{
		CategoryType: interfaces.CategoryTypeOther,
		CategoryName: categoryName,
	}
}

func (c *categoryManager) getCategorySystem(ctx context.Context) *interfaces.CategoryInfo {
	language := common.GetLanguageByCtx(ctx)
	tr := localize.NewI18nTranslator(language)
	categoryName := tr.Trans("category." + interfaces.CategoryTypeSystem.String())
	return &interfaces.CategoryInfo{
		CategoryType: interfaces.CategoryTypeSystem,
		CategoryName: categoryName,
	}
}

// UpdateCategory update category.
func (c *categoryManager) UpdateCategory(ctx context.Context, req *interfaces.UpdateCategoryReq) (resp *interfaces.UpdateCategoryResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	if err = c.requireOperatorTypePermission(ctx, req.UserID, interfaces.AuthOperationTypeModify); err != nil {
		return
	}
	// Check classification name.
	err = c.Validator.ValidatorCategoryName(ctx, req.CategoryName)
	if err != nil {
		return
	}
	// Check if default category exists.
	err = c.checkDefaultCategory(ctx, req.CategoryType.String(), req.CategoryName)
	if err != nil {
		return
	}
	// Check whether the classification exists. The type ID or type name cannot be repeated.
	categoryList, err := c.DBCategory.SelectListByCategoryIDOrName(ctx, nil, req.CategoryType.String(), req.CategoryName)
	if err != nil {
		return
	}

	if len(categoryList) == 0 {
		// The category does not exist and the error resource does not exist.
		return nil, errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtCategoryNotFound, "category_type: "+req.CategoryType.String()+" not found")
	} else if len(categoryList) > 1 {
		// There are multiple categories, and the error resource name is repeated.
		return nil, errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtCategoryNameExist, "name: "+req.CategoryName+"  already exists")
	} else if categoryList[0].CategoryID != req.CategoryType.String() {
		// Category ID does not match, error resource does not exist.
		return nil, errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtCategoryNotFound, "category_type: "+req.CategoryType.String()+" not found")
	}

	category := &model.CategoryDB{
		CategoryID:   req.CategoryType.String(),
		CategoryName: req.CategoryName,
		UpdateUser:   req.UserID,
	}

	err = c.DBCategory.UpdateByID(ctx, nil, category)
	if err != nil {
		return
	}
	resp = &interfaces.UpdateCategoryResp{
		CategoryType: req.CategoryType,
		CategoryName: req.CategoryName,
	}
	// Update category names in cache.
	c.Cache.Set(req.CategoryType.String(), req.CategoryName)
	return
}

// CreateCategory creates a category.
func (c *categoryManager) CreateCategory(ctx context.Context, req *interfaces.CreateCategoryReq) (resp *interfaces.CreateCategoryResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	if err = c.requireOperatorTypePermission(ctx, req.UserID, interfaces.AuthOperationTypeCreate); err != nil {
		return
	}
	// Check classification name.
	err = c.Validator.ValidatorCategoryName(ctx, req.CategoryName)
	if err != nil {
		return
	}
	// Check if default category exists.
	err = c.checkDefaultCategory(ctx, req.CategoryType.String(), req.CategoryName)
	if err != nil {
		return
	}
	// Check whether the classification exists. The type ID or type name cannot be repeated.
	err = c.checkDuplicatedCategory(ctx, req.CategoryType.String(), req.CategoryName)
	if err != nil {
		return
	}
	resp, err = c.insertCategory(ctx, nil, req)
	if err != nil {
		return
	}
	// Store category names in cache.
	c.Cache.Set(req.CategoryType.String(), req.CategoryName)
	return
}

// BatchCreateCategory creates categories in batches.
func (c *categoryManager) BatchCreateCategory(ctx context.Context, req []*interfaces.CreateCategoryReq) (resp []*interfaces.CreateCategoryResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	tx, err := c.DBTx.GetTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		} else {
			_ = tx.Commit()
		}
	}()
	resp = make([]*interfaces.CreateCategoryResp, 0, len(req))
	for _, req := range req {
		// Check whether the classification exists. The type ID or type name cannot be repeated. If it exists, skip it.
		categoryList, err := c.DBCategory.SelectListByCategoryIDOrName(ctx, nil, req.CategoryType.String(), req.CategoryName)
		if err != nil {
			return nil, err
		}
		if len(categoryList) > 0 {
			continue
		}
		respItem, err := c.insertCategory(ctx, tx, req)
		if err != nil {
			return nil, err
		}
		// Store category names in cache.
		c.Cache.Set(req.CategoryType.String(), req.CategoryName)
		resp = append(resp, respItem)
	}
	return
}

// DeleteCategory Delete category.
func (c *categoryManager) DeleteCategory(ctx context.Context, req *interfaces.DeleteCategoryReq) (err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	if err = c.requireOperatorTypePermission(ctx, req.UserID, interfaces.AuthOperationTypeDelete); err != nil {
		return
	}
	if string(req.CategoryType) == interfaces.CategoryTypeOther.String() {
		return errors.DefaultHTTPError(ctx, http.StatusForbidden, "category_type: "+interfaces.CategoryTypeOther.String()+" is a built-in system category and cannot be deleted")
	}
	var categoryDB *model.CategoryDB
	categoryDB, err = c.DBCategory.SelectListByCategoryID(ctx, nil, string(req.CategoryType))
	if err != nil {
		c.logger.Errorf("[DeleteCategory] select list by category id failed, err: %v", err)
		return err
	}
	if categoryDB == nil {
		return errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtCategoryNotFound, "category_type: "+string(req.CategoryType)+" not found")
	}
	err = c.DBCategory.DeleteByCategoryID(ctx, nil, string(req.CategoryType))
	if err != nil {
		c.logger.Errorf("[DeleteCategory] delete by category id failed, err: %v", err)
		return
	}
	// Delete category names from cache.
	c.Cache.Delete(string(req.CategoryType))
	return
}

func (c *categoryManager) insertCategory(ctx context.Context, tx *sql.Tx, req *interfaces.CreateCategoryReq) (resp *interfaces.CreateCategoryResp, err error) {
	category := &model.CategoryDB{
		CategoryID:   req.CategoryType.String(),
		CategoryName: req.CategoryName,
		CreateUser:   req.UserID,
		CreateTime:   time.Now().UnixNano(),
	}

	categoryID, err := c.DBCategory.Insert(ctx, tx, category)
	if err != nil {
		c.logger.WithContext(ctx).Errorf("[insertCategory] insert failed, err: %v", err)
		return
	}
	resp = &interfaces.CreateCategoryResp{
		CategoryType: interfaces.BizCategory(categoryID),
		CategoryName: req.CategoryName,
	}
	return
}

// checkDuplicatedCategory checks whether the category exists. The type ID or type name cannot be repeated.
func (c *categoryManager) checkDuplicatedCategory(ctx context.Context, categoryID, categoryName string) (err error) {
	categoryList, err := c.DBCategory.SelectListByCategoryIDOrName(ctx, nil, categoryID, categoryName)
	if err != nil {
		return
	}
	if len(categoryList) > 0 {
		for _, categoryItem := range categoryList {
			if categoryItem.CategoryID == categoryID {
				err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtCategoryNameExist, "category_type: "+categoryID+" name already exists")
				return
			}
			if categoryItem.CategoryName == categoryName {
				err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtCategoryNameExist, "name: "+categoryName+" already exists")
				return
			}
		}
	}
	return nil
}

// checkDefaultCategory checks whether the default category exists.
func (c *categoryManager) checkDefaultCategory(ctx context.Context, categoryID, categoryName string) (err error) {
	otherCategory := c.getCategoryOther(ctx)
	if otherCategory.CategoryType.String() == categoryID || otherCategory.CategoryName == categoryName {
		// Other categories are built-in categories in the system and are not allowed to be created.
		return errors.DefaultHTTPError(ctx, http.StatusBadRequest, "category_type: "+interfaces.CategoryTypeOther.String()+" is a built-in system category and cannot be created or modified")
	}
	systemCategory := c.getCategorySystem(ctx)
	if systemCategory.CategoryType.String() == categoryID || systemCategory.CategoryName == categoryName {
		// System built-in categories are system built-in categories and are not allowed to be created.
		return errors.DefaultHTTPError(ctx, http.StatusBadRequest, "category_type: "+interfaces.CategoryTypeSystem.String()+" is a built-in system category and cannot be created or modified")
	}
	return nil
}
