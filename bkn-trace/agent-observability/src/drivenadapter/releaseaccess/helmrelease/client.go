package helmrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceconfig"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/traceconfigmanifest"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecCommands struct{}

func (ExecCommands) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

type Client struct {
	commands  CommandRunner
	namespace string
	services  map[string]traceconfigmanifest.Service
}

func New(commands CommandRunner, namespace string, manifest traceconfigmanifest.Manifest) (*Client, error) {
	if commands == nil || strings.TrimSpace(namespace) == "" || strings.ContainsAny(namespace, " /\\") {
		return nil, fmt.Errorf("managed release client configuration is invalid")
	}
	services := make(map[string]traceconfigmanifest.Service, len(manifest.Services))
	for _, service := range manifest.Services {
		if _, err := service.Overrides(false); err != nil {
			return nil, err
		}
		services[service.Name] = service
	}
	return &Client{commands: commands, namespace: namespace, services: services}, nil
}

func (client *Client) Snapshot(ctx context.Context, service traceconfig.Service) (traceconfig.Snapshot, error) {
	managed, err := client.managed(service.Name)
	if err != nil {
		return traceconfig.Snapshot{}, err
	}
	output, err := client.commands.Run(ctx, "helm", "status", managed.Release, "--namespace", client.namespace, "-o", "json")
	if err != nil {
		return traceconfig.Snapshot{}, err
	}
	var status struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(output, &status); err != nil || status.Version <= 0 {
		return traceconfig.Snapshot{}, fmt.Errorf("invalid Helm status for %s", managed.Release)
	}
	return traceconfig.Snapshot{Service: service.Name, Revision: status.Version}, nil
}

func (client *Client) Apply(ctx context.Context, service traceconfig.Service, revision int, enabled bool) error {
	managed, err := client.managed(service.Name)
	if err != nil {
		return err
	}
	overrides, err := managed.Overrides(enabled)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := []string{"upgrade", "--install", managed.Release, managed.Chart, "--namespace", client.namespace, "--reuse-values"}
	for _, key := range keys {
		args = append(args, "--set", key+"="+strconv.FormatBool(overrides[key]))
	}
	args = append(args, "--set-string", "traceEvidence.revision="+strconv.Itoa(revision), "--wait", "--timeout", "10m")
	_, err = client.commands.Run(ctx, "helm", args...)
	return err
}

func (client *Client) WaitReady(ctx context.Context, service traceconfig.Service, _ int, _ bool) error {
	managed, err := client.managed(service.Name)
	if err != nil {
		return err
	}
	output, err := client.commands.Run(ctx, "helm", "status", managed.Release, "--namespace", client.namespace, "-o", "json")
	if err != nil {
		return err
	}
	var status struct {
		Info struct {
			Status string `json:"status"`
		} `json:"info"`
	}
	if err := json.Unmarshal(output, &status); err != nil || status.Info.Status != "deployed" {
		return fmt.Errorf("helm release %s is not deployed", managed.Release)
	}
	return nil
}

func (client *Client) Rollback(ctx context.Context, snapshot traceconfig.Snapshot) error {
	managed, err := client.managed(snapshot.Service)
	if err != nil {
		return err
	}
	_, err = client.commands.Run(ctx, "helm", "rollback", managed.Release, strconv.Itoa(snapshot.Revision), "--namespace", client.namespace, "--wait", "--timeout", "10m")
	return err
}

func (client *Client) managed(name string) (traceconfigmanifest.Service, error) {
	service, ok := client.services[name]
	if !ok {
		return traceconfigmanifest.Service{}, fmt.Errorf("release target %q is not managed", name)
	}
	return service, nil
}
