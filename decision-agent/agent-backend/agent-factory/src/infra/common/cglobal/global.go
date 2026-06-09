package cglobal

import (
	"github.com/kweaver-ai/proton-rds-sdk-go/sqlx"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/cconf"
)

var (
	GConfig *cconf.Config // 全局配置
	GDB     *sqlx.DB      // 全局 DB
)
