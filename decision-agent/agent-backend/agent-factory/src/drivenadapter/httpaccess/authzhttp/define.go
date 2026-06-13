package authzhttp

import (
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/cmp/icmp"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/cglobal"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/cutil"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driven/ihttpaccess/iauthzacc"
)

type authZHttpAcc struct {
	logger         icmp.Logger
	privateBaseURL string
	publicBaseURL  string
}

var _ iauthzacc.AuthZHttpAcc = &authZHttpAcc{}

func NewAuthZHttpAcc(
	logger icmp.Logger,
) iauthzacc.AuthZHttpAcc {
	// 从配置中获取授权服务的地址
	authZPrivConf := cglobal.GConfig.Authorization.PrivateSvc
	authZPubConf := cglobal.GConfig.Authorization.PublicSvc

	privateBaseURL := cutil.GetHTTPAccess(authZPrivConf.Host, authZPrivConf.Port, authZPrivConf.Protocol)

	publicBaseURL := cutil.GetHTTPAccess(authZPubConf.Host, authZPubConf.Port, authZPubConf.Protocol)

	authZImpl := &authZHttpAcc{
		logger:         logger,
		privateBaseURL: privateBaseURL,
		publicBaseURL:  publicBaseURL,
	}

	// 经 AUTHZ_PROVIDER 开关选择 authz 后端。此前直接返回 ISF 实现，导致
	// MaybeShadow 形同虚设：即便 AUTHZ_PROVIDER=bkn-safe，InitPermission 等
	// 仍打已退役的 ISF authorization-private 端点而 panic。
	return MaybeShadow(authZImpl, logger)
}
