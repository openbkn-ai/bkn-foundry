package traceconfig

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeReleaseClient struct {
	calls      []string
	failTarget string
}

func (client *fakeReleaseClient) Snapshot(_ context.Context, service Service) (Snapshot, error) {
	client.calls = append(client.calls, "snapshot:"+service.Name)
	return Snapshot{Service: service.Name, Revision: 4}, nil
}

func (client *fakeReleaseClient) Apply(_ context.Context, service Service, revision int, enabled bool) error {
	client.calls = append(client.calls, "apply:"+service.Name+":"+boolString(enabled))
	if service.Name == client.failTarget && enabled {
		return errors.New("release failed")
	}
	return nil
}

func (client *fakeReleaseClient) WaitReady(_ context.Context, service Service, revision int, enabled bool) error {
	client.calls = append(client.calls, "ready:"+service.Name+":"+boolString(enabled))
	return nil
}

func (client *fakeReleaseClient) Rollback(_ context.Context, snapshot Snapshot) error {
	client.calls = append(client.calls, "rollback:"+snapshot.Service)
	return nil
}

func TestReleaseRunnerAppliesOnlyManifestServicesAndWaitsForReady(t *testing.T) {
	client := &fakeReleaseClient{}
	runner := NewReleaseRunner(client, []Service{
		{Name: "bkn-backend", Critical: true},
		{Name: "ontology-query", Critical: true},
	})

	result := runner.Run(context.Background(), Request{Revision: 5, Enabled: true})
	if result.Phase != PhaseSucceeded {
		t.Fatalf("phase = %s, want %s", result.Phase, PhaseSucceeded)
	}
	want := []string{
		"snapshot:bkn-backend", "apply:bkn-backend:true", "ready:bkn-backend:true",
		"snapshot:ontology-query", "apply:ontology-query:true", "ready:ontology-query:true",
	}
	if !reflect.DeepEqual(client.calls, want) {
		t.Fatalf("calls = %#v, want %#v", client.calls, want)
	}
}

func TestReleaseRunnerRollsBackChangedServicesInReverseOrder(t *testing.T) {
	client := &fakeReleaseClient{failTarget: "ontology-query"}
	runner := NewReleaseRunner(client, []Service{
		{Name: "bkn-backend", Critical: true},
		{Name: "ontology-query", Critical: true},
		{Name: "vega-backend", Critical: true},
	})

	result := runner.Run(context.Background(), Request{Revision: 5, Enabled: true})
	if result.Phase != PhaseRolledBack {
		t.Fatalf("phase = %s, want %s", result.Phase, PhaseRolledBack)
	}
	want := []string{
		"snapshot:bkn-backend", "apply:bkn-backend:true", "ready:bkn-backend:true",
		"snapshot:ontology-query", "apply:ontology-query:true",
		"rollback:ontology-query", "rollback:bkn-backend",
	}
	if !reflect.DeepEqual(client.calls, want) {
		t.Fatalf("calls = %#v, want %#v", client.calls, want)
	}
}

func TestReleaseRunnerReusesPersistedSnapshotsAfterControllerRestart(t *testing.T) {
	client := &fakeReleaseClient{}
	runner := NewReleaseRunner(client, []Service{{Name: "bkn-backend", Critical: true}})

	result := runner.Run(context.Background(), Request{
		Revision: 5, Enabled: true,
		Snapshots: map[string]Snapshot{"bkn-backend": {Service: "bkn-backend", Revision: 2}},
	})
	if result.Phase != PhaseSucceeded {
		t.Fatalf("phase = %s, want %s", result.Phase, PhaseSucceeded)
	}
	want := []string{"apply:bkn-backend:true", "ready:bkn-backend:true"}
	if !reflect.DeepEqual(client.calls, want) {
		t.Fatalf("restart must not replace original snapshot: calls=%#v want=%#v", client.calls, want)
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
