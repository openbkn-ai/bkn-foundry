package dainject

import (
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/cconf"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/cutil"
)

func getModelApiUrlPrefix(_conf *cconf.ModelFactoryConf) (urlPrefix string) {
	apiSvc := _conf.ModelApiSvc
	host := cutil.ParseHost(apiSvc.Host)

	urlPrefix = fmt.Sprintf("%s://%s:%d/api/private/mf-model-api/v1", apiSvc.Protocol, host, apiSvc.Port)

	return
}
