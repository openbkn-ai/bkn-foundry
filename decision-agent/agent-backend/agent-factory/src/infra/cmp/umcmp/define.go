package umcmp

import (
	"os"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/cconf"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/cmp/icmp"
	// "github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/config"
)

type Um struct {
	umConf *cconf.UserMgntCfg

	logger icmp.Logger

	// bkn-safe directory cutover (revertible): DIRECTORY_PROVIDER=bkn-safe +
	// BKN_SAFE_URL route every method to bkn-safe. Unset to revert (default ISF).
	directoryProvider string
	bknSafeURL        string
}

func NewUmCmp(umConf *cconf.UserMgntCfg,
	logger icmp.Logger,
) *Um {
	return &Um{
		umConf:            umConf,
		logger:            logger,
		directoryProvider: os.Getenv("DIRECTORY_PROVIDER"),
		bknSafeURL:        os.Getenv("BKN_SAFE_URL"),
	}
}
