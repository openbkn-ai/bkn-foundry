package dainject

import (
	"sync"

	"github.com/kweaver-ai/kweaver-go-lib/logger"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/service"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/service/bizdomainsvc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/chttpinject"
)

var (
	bizDomainSvcOnce sync.Once
	bizDomainSvcImpl *bizdomainsvc.BizDomainSvc
)

// NewBizDomainSvc 创建业务域服务实例
func NewBizDomainSvc() *bizdomainsvc.BizDomainSvc {
	bizDomainSvcOnce.Do(func() {
		dto := &bizdomainsvc.NewBizDomainSvcDto{
			SvcBase:       service.NewSvcBase(),
			Logger:        logger.GetLogger(),
			BizDomainHttp: chttpinject.NewBizDomainHttpAcc(),
		}

		bizDomainSvcImpl = bizdomainsvc.NewBizDomainService(dto)
	})

	return bizDomainSvcImpl
}
