package main

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/traceconfigmanifest"
)

func TestServicesFromManifestPreservesReviewedOrderAndCriticality(t *testing.T) {
	services := servicesFromManifest(traceconfigmanifest.Manifest{Services: []traceconfigmanifest.Service{
		{Name: "bkn-backend", Critical: true}, {Name: "optional", Critical: false},
	}})
	if len(services) != 2 || services[0].Name != "bkn-backend" || !services[0].Critical || services[1].Critical {
		t.Fatalf("unexpected services: %+v", services)
	}
}
