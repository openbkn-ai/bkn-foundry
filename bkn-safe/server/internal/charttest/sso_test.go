// Package charttest verifies first-install and upgrade wiring without a cluster.
package charttest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type envVar struct {
	Name      string `yaml:"name"`
	Value     string `yaml:"value"`
	ValueFrom struct {
		SecretKeyRef struct{ Name, Key string } `yaml:"secretKeyRef"`
	} `yaml:"valueFrom"`
}
type object struct {
	Kind     string                           `yaml:"kind"`
	Metadata struct{ Name, Namespace string } `yaml:"metadata"`
	Data     map[string]string                `yaml:"data"`
	Spec     struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Name string
					Env  []envVar
				}
			}
		}
	} `yaml:"spec"`
}

func render(t *testing.T, args ...string) ([]object, error) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is required for chart regression tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	params := append([]string{"template", "customer-safe", "../../../charts/bkn-safe", "--namespace", "customer"}, args...)
	cmd := exec.CommandContext(ctx, "helm", params...)
	// Never contact the developer's actual cluster, including during lookup tests.
	cmd.Env = append(os.Environ(), "KUBECONFIG="+filepath.Join(t.TempDir(), "nonexistent"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, &renderError{message: string(out)}
	}
	dec := yaml.NewDecoder(strings.NewReader(string(out)))
	var objects []object
	for {
		var obj object
		if err := dec.Decode(&obj); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		objects = append(objects, obj)
	}
	return objects, nil
}

type renderError struct{ message string }

func (e *renderError) Error() string { return e.message }
func find(t *testing.T, objects []object, kind, name string) object {
	t.Helper()
	for _, obj := range objects {
		if obj.Kind == kind && obj.Metadata.Name == name {
			return obj
		}
	}
	t.Fatalf("missing %s %s", kind, name)
	return object{}
}
func envs(obj object) map[string]envVar {
	result := map[string]envVar{}
	for _, c := range obj.Spec.Template.Spec.Containers {
		for _, e := range c.Env {
			result[e.Name] = e
		}
	}
	return result
}
func TestSSOFreshInstall(t *testing.T) {
	objects, err := render(t)
	if err != nil {
		t.Fatal(err)
	}
	secret := find(t, objects, "Secret", "customer-safe-sso")
	if len(secret.Data) != 3 {
		t.Fatal("incomplete generated credentials")
	}
	for key, value := range secret.Data {
		raw, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(raw) < 32 {
			t.Fatalf("invalid generated credential %s", key)
		}
		if key == "assertionPrivateKeyPEM" && !strings.Contains(string(raw), "PRIVATE KEY") {
			t.Fatal("missing PEM key")
		}
	}
	safe := envs(find(t, objects, "Deployment", "bkn-safe"))
	hydra := envs(find(t, objects, "Deployment", "customer-safe-hydra"))
	if safe["SAFE_SSO_AUTO_BOOTSTRAP"].Value != "true" {
		t.Fatal("setup not enabled")
	}
	if hydra["OAUTH2_TOKEN_HOOK_URL"].Value != "http://bkn-safe:3000/internal/sso/token-hook" {
		t.Fatal("hook must use Service name, even with renamed release")
	}
	for _, name := range []string{"SAFE_SSO_HYDRA_CLIENT_SECRET", "SAFE_SSO_ASSERTION_PRIVATE_KEY_PEM", "SAFE_SSO_HOOK_KEY"} {
		if safe[name].ValueFrom.SecretKeyRef.Name != "customer-safe-sso" {
			t.Fatalf("missing Secret reference %s", name)
		}
	}
	if !reflect.DeepEqual(safe["SAFE_SSO_HOOK_KEY"].ValueFrom, hydra["OAUTH2_TOKEN_HOOK_AUTH_CONFIG_VALUE"].ValueFrom) {
		t.Fatal("hook keys differ")
	}
}
func TestSSODisabledAndExternalSecret(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		secret string
	}{
		{"disabled", []string{"--set", "bundledDeps.ssoTokenHook.enabled=false"}, ""},
		{"external", []string{"--set", "bundledDeps.ssoTokenHook.secretName=customer-existing", "--set", "bundledDeps.ssoTokenHook.secretKey=custom-hook"}, "customer-existing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects, err := render(t, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			for _, obj := range objects {
				if obj.Kind == "Secret" && obj.Metadata.Name == "customer-safe-sso" {
					t.Fatal("must not generate managed secret")
				}
			}
			safe := envs(find(t, objects, "Deployment", "bkn-safe"))
			hydra := envs(find(t, objects, "Deployment", "customer-safe-hydra"))
			if tc.secret == "" {
				if _, ok := safe["SAFE_SSO_HOOK_KEY"]; ok {
					t.Fatal("disabled app still configured")
				}
				if _, ok := hydra["OAUTH2_TOKEN_HOOK_URL"]; ok {
					t.Fatal("disabled Hydra still configured")
				}
			} else {
				ref := safe["SAFE_SSO_HOOK_KEY"].ValueFrom.SecretKeyRef
				if ref.Name != tc.secret || ref.Key != "custom-hook" {
					t.Fatal("external credentials not wired")
				}
			}
		})
	}
}
func TestSSOUpgradePreservesCredentials(t *testing.T) {
	data := map[string]string{}
	for _, key := range []string{"hookKey", "hydraClientSecret", "assertionPrivateKeyPEM"} {
		data[key] = base64.StdEncoding.EncodeToString([]byte("original-" + key))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/version":
			_ = json.NewEncoder(w).Encode(map[string]string{"gitVersion": "v1.30.0", "major": "1", "minor": "30"})
		case "/api":
			_, _ = w.Write([]byte(`{"kind":"APIVersions","apiVersion":"v1","versions":["v1"]}`))
		case "/apis":
			_, _ = w.Write([]byte(`{"kind":"APIGroupList","apiVersion":"v1","groups":[{"name":"apps","versions":[{"groupVersion":"apps/v1","version":"v1"}],"preferredVersion":{"groupVersion":"apps/v1","version":"v1"}}]}`))
		case "/api/v1":
			_, _ = w.Write([]byte(`{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[{"name":"secrets","kind":"Secret","namespaced":true},{"name":"configmaps","kind":"ConfigMap","namespaced":true}]}`))
		case "/apis/apps/v1":
			_, _ = w.Write([]byte(`{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"apps/v1","resources":[{"name":"deployments","kind":"Deployment","namespaced":true}]}`))
		case "/api/v1/namespaces/customer/secrets/customer-safe-sso":
			_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]string{"name": "customer-safe-sso", "namespace": "customer"}, "data": data})
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"NotFound","code":404}`))
		}
	}))
	defer srv.Close()
	config := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(config, []byte("apiVersion: v1\nkind: Config\nclusters:\n- name: fake\n  cluster:\n    server: "+srv.URL+"\ncontexts:\n- name: fake\n  context:\n    cluster: fake\n    user: fake\ncurrent-context: fake\nusers:\n- name: fake\n  user: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	objects, err := render(t, "--dry-run=server", "--kubeconfig", config)
	if err != nil {
		t.Fatal(err)
	}
	secret := find(t, objects, "Secret", "customer-safe-sso")
	if !reflect.DeepEqual(secret.Data, data) {
		t.Fatal("upgrade rotated credentials")
	}
	// A damaged Secret must stop the upgrade rather than replace lost keys.
	delete(data, "assertionPrivateKeyPEM")
	_, err = render(t, "--dry-run=server", "--kubeconfig", config)
	if err == nil || !strings.Contains(err.Error(), "restore the original Secret") {
		t.Fatal("missing key was silently regenerated")
	}
}

func TestSSOExplicitEnvironmentOverridesAreUnique(t *testing.T) {
	objects, err := render(t, "--set", "extraEnv[0].name=SAFE_SSO_AUTO_BOOTSTRAP", "--set-string", "extraEnv[0].value=false")
	if err != nil {
		t.Fatal(err)
	}
	obj := find(t, objects, "Deployment", "bkn-safe")
	names := map[string]bool{}
	for _, container := range obj.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			if names[env.Name] {
				t.Fatalf("duplicate environment variable %s", env.Name)
			}
			names[env.Name] = true
		}
	}
	if envs(obj)["SAFE_SSO_AUTO_BOOTSTRAP"].Value != "false" {
		t.Fatal("explicit setting overridden")
	}
}
