package bknsafeaccess

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iauthorizationscope"
)

func TestResolveBuildsProfileFromCurrentSafeIdentityAndNetworkGrants(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer current-token" {
			t.Fatalf("authorization was not forwarded to BKN Safe")
		}
		switch r.URL.Path {
		case "/api/safe/v1/me":
			_, _ = w.Write([]byte(`{"id":"actor-a","account_type":"user","enabled":true,"roles":["network_builder","unknown-role"]}`))
		case "/api/safe/v1/me/permissions":
			_, _ = w.Write([]byte(`{"is_admin":true,"permissions":[
				{"resource":{"type":"*","id":"*"},"operations":["*"]},
				{"resource":{"type":"knowledge_network","id":"*"},"operations":["modify"]},
				{"resource":{"type":"knowledge_network","id":"kn-b"},"operations":["view_detail","task_manage"]},
				{"resource":{"type":"knowledge_network","id":"kn-a"},"operations":["authorize"]},
				{"resource":{"type":"knowledge_network","id":"kn-c"},"operations":["view_detail"]}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := New(server.URL, server.Client())
	profile, err := client.Resolve(context.Background(), "Bearer current-token", iauthorizationscope.TrustedIdentity{
		TenantID: "tenant-a", BusinessDomain: "domain-a", ActorID: "actor-a",
		EffectiveSubjectID: "user-a", ApplicationPrincipalID: "app-a", DelegationID: "delegation-a",
	})
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if !profile.AccountActive || !profile.TenantActive || profile.ActorID != "actor-a" ||
		profile.EffectiveSubjectID != "user-a" || profile.ApplicationPrincipalID != "app-a" {
		t.Fatalf("unexpected trusted identity projection: %+v", profile)
	}
	if !reflect.DeepEqual(profile.Roles, []string{"network_builder"}) {
		t.Fatalf("only current built-in roles may enter the profile: %v", profile.Roles)
	}
	if !reflect.DeepEqual(profile.ManagedKnowledgeNetworkIDs, []string{"kn-a", "kn-b"}) {
		t.Fatalf("only concrete management grants may enter the profile: %v", profile.ManagedKnowledgeNetworkIDs)
	}
	if !profile.ManagesAllKnowledgeNetworks {
		t.Fatal("network_builder knowledge_network:* management grant must enter the profile")
	}
	if profile.Fingerprint == "" {
		t.Fatal("access scope fingerprint is required")
	}
}

func TestResolveDoesNotTreatGlobalAdminWildcardAsNetworkManagement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/safe/v1/me":
			_, _ = w.Write([]byte(`{"id":"actor-a","enabled":true,"roles":["super_admin"]}`))
		case "/api/safe/v1/me/permissions":
			_, _ = w.Write([]byte(`{"permissions":[{"resource":{"type":"*","id":"*"},"operations":["*"]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	profile, err := New(server.URL, server.Client()).Resolve(
		context.Background(), "Bearer current-token", iauthorizationscope.TrustedIdentity{
			TenantID: "tenant-a", ActorID: "actor-a", EffectiveSubjectID: "actor-a",
		},
	)
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if profile.ManagesAllKnowledgeNetworks {
		t.Fatal("global super_admin wildcard must not imply knowledge network business access")
	}
}

func TestResolveFailsClosedForDisabledOrMismatchedIdentity(t *testing.T) {
	for _, body := range []string{
		`{"id":"actor-a","account_type":"user","enabled":false,"roles":["normal_user"]}`,
		`{"id":"different-actor","account_type":"user","enabled":true,"roles":["normal_user"]}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/safe/v1/me" {
				_, _ = w.Write([]byte(body))
				return
			}
			_, _ = w.Write([]byte(`{"permissions":[]}`))
		}))
		client := New(server.URL, server.Client())
		_, err := client.Resolve(context.Background(), "Bearer token", iauthorizationscope.TrustedIdentity{
			TenantID: "tenant-a", BusinessDomain: "domain-a", ActorID: "actor-a", EffectiveSubjectID: "user-a",
		})
		server.Close()
		if err == nil {
			t.Fatalf("disabled or mismatched current identity must fail closed: %s", body)
		}
	}
}

func TestResolveFingerprintIsStableAndChangesWithManagedScope(t *testing.T) {
	first := fingerprintInput{
		TenantID: "tenant-a", BusinessDomain: "domain-a", ActorID: "actor-a",
		EffectiveSubjectID: "user-a", Roles: []string{"normal_user", "network_builder"},
		ManagedKnowledgeNetworkIDs: []string{"kn-b", "kn-a"},
	}
	second := first
	second.Roles = []string{"network_builder", "normal_user"}
	second.ManagedKnowledgeNetworkIDs = []string{"kn-a", "kn-b"}
	if accessScopeFingerprint(first) != accessScopeFingerprint(second) {
		t.Fatal("equivalent access scopes must have the same fingerprint")
	}
	second.ManagedKnowledgeNetworkIDs = []string{"kn-a"}
	if accessScopeFingerprint(first) == accessScopeFingerprint(second) {
		t.Fatal("managed network revocation must change the fingerprint")
	}
	second = first
	second.ManagesAllKnowledgeNetworks = true
	if accessScopeFingerprint(first) == accessScopeFingerprint(second) {
		t.Fatal("type-wide network management must change the fingerprint")
	}
}
