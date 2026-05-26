package producte2p

import (
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/domain/entity/producteo"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/infra/common/cutil"
	"github.com/kowell-ai/kowell-core/decision-agent/agent-backend/agent-factory/src/infra/persistence/dapo"
)

// Product 将产品实体对象转换为持久化对象
func Product(eo *producteo.Product) (po *dapo.ProductPo, err error) {
	if eo == nil {
		return
	}

	po = &dapo.ProductPo{}

	err = cutil.CopyStructUseJSON(po, eo)
	if err != nil {
		return
	}

	return
}
