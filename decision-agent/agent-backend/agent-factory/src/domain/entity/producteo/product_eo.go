package producteo

import "github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/infra/persistence/dapo"

// Product 产品实体对象
type Product struct {
	dapo.ProductPo
}
