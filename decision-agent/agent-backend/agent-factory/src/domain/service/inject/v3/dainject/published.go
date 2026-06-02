package dainject

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/service"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/service/publishedsvc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/daconftpldbacc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/productdbacc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/pubedagentdbacc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/dbaccess/publishedtpldbacc"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/chttpinject"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driver/iv3portdriver"
)

var (
	publishedSvcOnce sync.Once
	publishedSvcImpl iv3portdriver.IPublishedSvc
)

// NewPublishedSvc .
func NewPublishedSvc() iv3portdriver.IPublishedSvc {
	publishedSvcOnce.Do(func() {
		dto := &publishedsvc.NewPublishedSvcDto{
			SvcBase:          service.NewSvcBase(),
			AgentTplRepo:     daconftpldbacc.NewDataAgentTplRepo(),
			PublishedTplRepo: publishedtpldbacc.NewPublishedTplRepo(),
			ProductRepo:      productdbacc.NewProductRepo(),
			UmHttp:           chttpinject.NewUmHttpAcc(),
			AuthZHttp:        chttpinject.NewAuthZHttpAcc(),
			PubedAgentRepo:   pubedagentdbacc.NewPubedAgentRepo(),
			PmsSvc:           NewPermissionSvc(),
			BizDomainHttp:    chttpinject.NewBizDomainHttpAcc(),
		}

		publishedSvcImpl = publishedsvc.NewPublishedService(dto)
	})

	return publishedSvcImpl
}
