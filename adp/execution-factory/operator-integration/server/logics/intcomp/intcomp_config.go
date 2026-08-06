// Package intcomp internal component config
// @file intcomp_config.go
// @description: 内置组件配置操作
package intcomp

import (
	"context"
	"database/sql"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/validator"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
)

// IntCompConfigImpl internal component config impl
type intCompConfigImpl struct {
	IntCompDB model.IInternalComponentConfigDB
	Logger    interfaces.Logger
	Validator interfaces.Validator
}

var (
	iOnce sync.Once
	ic    interfaces.IIntCompConfigService
)

// NewIntCompConfigService new internal component config service
func NewIntCompConfigService() interfaces.IIntCompConfigService {
	iOnce.Do(func() {
		confLoader := config.NewConfigLoader()
		ic = &intCompConfigImpl{
			IntCompDB: dbaccess.NewInternalComponentConfigDBSingleton(),
			Logger:    confLoader.GetLogger(),
			Validator: validator.NewValidator(),
		}
	})
	return ic
}

// DeleteConfig 删除配置
func (i *intCompConfigImpl) DeleteConfig(ctx context.Context, tx *sql.Tx, configType, configID string) (err error) {
	// 检查是否存在
	exist, _, err := i.IntCompDB.SelectConfig(ctx, configType, configID)
	if err != nil {
		i.Logger.WithContext(ctx).Errorf("select config failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		return
	}
	err = i.IntCompDB.DeleteConfig(ctx, tx, configType, configID)
	if err != nil {
		i.Logger.WithContext(ctx).Errorf("delete internal component config error: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	return
}
