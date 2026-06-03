package remote

import (
	"encoding/json"
	"testing"
)

func TestBuildRouteJSONSetsForwardedHTTPSHeaders(t *testing.T) {
	data, err := buildRouteJSON("app-test", []string{"*.example.com"}, []string{"app-test:8080"}, RouteOptions{
		ForwardedProto: "https",
		ForwardedSSL:   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var route map[string]interface{}
	if err := json.Unmarshal(data, &route); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	handles := route["handle"].([]interface{})
	proxy := handles[0].(map[string]interface{})
	headers := proxy["headers"].(map[string]interface{})
	request := headers["request"].(map[string]interface{})
	set := request["set"].(map[string]interface{})

	if got := set["X-Forwarded-Proto"].([]interface{})[0]; got != "https" {
		t.Fatalf("expected X-Forwarded-Proto=https, got %v", got)
	}
	if got := set["X-Forwarded-Ssl"].([]interface{})[0]; got != "on" {
		t.Fatalf("expected X-Forwarded-Ssl=on, got %v", got)
	}
	if got := set["X-Forwarded-Port"].([]interface{})[0]; got != "443" {
		t.Fatalf("expected X-Forwarded-Port=443, got %v", got)
	}
}

func TestCaddyDNSProviderForCloudflare(t *testing.T) {
	provider, err := CaddyDNSProviderFor("cloudflare")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.Name != "cloudflare" {
		t.Fatalf("expected cloudflare provider, got %q", provider.Name)
	}
	if provider.Module != "github.com/caddy-dns/cloudflare" {
		t.Fatalf("unexpected module: %q", provider.Module)
	}
	if provider.TokenEnv != "CLOUDFLARE_API_TOKEN" {
		t.Fatalf("unexpected token env: %q", provider.TokenEnv)
	}
}

func TestWildcardDomain(t *testing.T) {
	if got := WildcardDomain("gatherpro.events"); got != "*.gatherpro.events" {
		t.Fatalf("unexpected wildcard: %q", got)
	}
	if got := WildcardDomain("*.gatherpro.events"); got != "*.gatherpro.events" {
		t.Fatalf("unexpected wildcard normalization: %q", got)
	}
}

func TestSetAutoHTTPSSkip(t *testing.T) {
	server := map[string]interface{}{"routes": []interface{}{}}
	setAutoHTTPSSkip(server, []string{"*.example.com"})

	got := autoHTTPSSkip(server)
	if len(got) != 1 || got[0] != "*.example.com" {
		t.Fatalf("unexpected skip list: %#v", got)
	}

	setAutoHTTPSSkip(server, nil)
	if got := autoHTTPSSkip(server); len(got) != 0 {
		t.Fatalf("expected empty skip list, got %#v", got)
	}
	if _, ok := server["automatic_https"]; ok {
		t.Fatal("expected empty automatic_https object to be removed")
	}
}

func TestBuildOnDemandAutomationJSON(t *testing.T) {
	data, err := buildOnDemandAutomationJSON("gatherpro.events", "http://app-gatherhub:8080/_neo/caddy/ask")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !tlsConfigHasOnDemandWildcard([]byte(`{"automation":`+string(data)+`}`), "gatherpro.events") {
		t.Fatal("expected generated on-demand automation to allow wildcard")
	}
}

func TestTLSPolicyManagesBase(t *testing.T) {
	prod := map[string]interface{}{
		"subjects": []interface{}{"gatherpro.events", "*.gatherpro.events"},
	}
	staging := map[string]interface{}{
		"subjects": []interface{}{"staging.gatherpro.events", "*.staging.gatherpro.events"},
	}

	// The prod policy manages the prod base, not the staging base — so merging a
	// staging policy must not match (and therefore not replace) the prod policy.
	if !tlsPolicyManagesBase(prod, "gatherpro.events", "*.gatherpro.events") {
		t.Fatal("expected prod policy to manage gatherpro.events")
	}
	if tlsPolicyManagesBase(prod, "staging.gatherpro.events", "*.staging.gatherpro.events") {
		t.Fatal("prod policy must NOT be treated as managing the staging tree")
	}
	if !tlsPolicyManagesBase(staging, "staging.gatherpro.events", "*.staging.gatherpro.events") {
		t.Fatal("expected staging policy to manage staging.gatherpro.events")
	}
	// A policy missing the wildcard does not "manage" the tree.
	partial := map[string]interface{}{"subjects": []interface{}{"gatherpro.events"}}
	if tlsPolicyManagesBase(partial, "gatherpro.events", "*.gatherpro.events") {
		t.Fatal("policy without the wildcard subject must not match")
	}
}

func TestTLSConfigHasOnDemandWildcard(t *testing.T) {
	data := []byte(`{
		"automation": {
			"on_demand": {
				"permission": {
					"module": "http",
					"endpoint": "http://app:8080/_neo/caddy/ask"
				}
			},
			"policies": [{
				"on_demand": true,
				"subjects": ["gatherpro.events", "*.gatherpro.events"]
			}]
		}
	}`)

	if !tlsConfigHasOnDemandWildcard(data, "gatherpro.events") {
		t.Fatal("expected on-demand wildcard config to be detected")
	}
	if tlsConfigHasOnDemandWildcard(data, "example.com") {
		t.Fatal("did not expect unrelated wildcard config to match")
	}
}
