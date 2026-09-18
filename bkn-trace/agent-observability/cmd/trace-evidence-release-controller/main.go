package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceconfig"
	filetraceconfigstore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/fileaccess/traceconfigstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/releaseaccess/helmrelease"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/traceconfigmanifest"
)

func main() {
	config := conf.NewTraceReleaseConfig()
	manifestFile, err := os.Open(config.ManifestPath)
	if err != nil {
		log.Fatalf("open managed release manifest: %v", err)
	}
	manifest, err := traceconfigmanifest.Load(manifestFile)
	_ = manifestFile.Close()
	if err != nil {
		log.Fatalf("load managed release manifest: %v", err)
	}
	client, err := helmrelease.New(helmrelease.ExecCommands{}, config.Namespace, manifest)
	if err != nil {
		log.Fatalf("configure managed release client: %v", err)
	}
	store := filetraceconfigstore.New(config.StatePath)
	controller := traceconfig.NewController(store, traceconfig.NewReleaseRunner(client, servicesFromManifest(manifest)))
	if err := controller.RecoverInterrupted(context.Background()); err != nil {
		log.Fatalf("recover interrupted trace evidence release: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()
	for {
		processed, err := controller.ProcessOne(ctx)
		if err != nil {
			log.Printf("trace evidence release completed with error: %v", err)
		} else if processed {
			log.Printf("trace evidence release completed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func servicesFromManifest(manifest traceconfigmanifest.Manifest) []traceconfig.Service {
	services := make([]traceconfig.Service, 0, len(manifest.Services))
	for _, service := range manifest.Services {
		services = append(services, traceconfig.Service{Name: service.Name, Critical: service.Critical})
	}
	return services
}
