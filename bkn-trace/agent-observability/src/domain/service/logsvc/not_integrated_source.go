package logsvc

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type NotIntegratedSource struct {
	id         string
	categories []string
	modules    []string
}

func NewNotIntegratedSource(id string, categories, modules []string) *NotIntegratedSource {
	return &NotIntegratedSource{
		id: id, categories: append([]string(nil), categories...), modules: append([]string(nil), modules...),
	}
}

func (source *NotIntegratedSource) ID() string { return source.id }

func (source *NotIntegratedSource) Search(context.Context, observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	return observabilityvo.SourcePage{}, ErrSourcesUnavailable
}

func (source *NotIntegratedSource) Metadata() observabilityvo.SourceStatus {
	return observabilityvo.SourceStatus{
		SourceID: source.id, Status: "not_integrated", Reason: "source_not_integrated",
		Reliability: "best_effort", CollectionMethod: "not_integrated",
		CoveredModules: append([]string(nil), source.modules...), CountAccuracy: "unavailable",
		Categories: append([]string(nil), source.categories...),
	}
}
