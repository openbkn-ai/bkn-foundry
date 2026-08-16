// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package business_system

import (
	"context"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

// NoopBusinessSystemService is used when business domains are disabled.
type NoopBusinessSystemService struct {
	appSetting *common.AppSetting
}

func NewNoopBusinessSystemService(appSetting *common.AppSetting) interfaces.BusinessSystemService {
	return &NoopBusinessSystemService{appSetting: appSetting}
}

func (n *NoopBusinessSystemService) BindResource(ctx context.Context, bd_id string, rid string, rtype string) error {
	return nil // Silently skip.
}

func (n *NoopBusinessSystemService) UnbindResource(ctx context.Context, bd_id string, rid string, rtype string) error {
	return nil // Silently skip.
}
