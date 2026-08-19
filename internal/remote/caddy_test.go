package remote

import (
	"encoding/json"
	"strings"
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

func TestIsDNSCaddyImage(t *testing.T) {
	cases := map[string]bool{
		"neo-caddy-dns-cloudflare:latest": true,
		"neo-caddy-dns-route53:latest":    true,
		dnsCaddyImagePrefix + "x":         true,
		CaddyImage:                        false,
		"caddy:latest":                    false,
		"":                                false,
		"nginx:latest":                    false,
	}
	for image, want := range cases {
		if got := isDNSCaddyImage(image); got != want {
			t.Errorf("isDNSCaddyImage(%q) = %v, want %v", image, got, want)
		}
	}
}

func TestInspectHandlerPlainReverseProxy(t *testing.T) {
	auth, ups := inspectHandler([]byte(`{"handler":"reverse_proxy","upstreams":[{"dial":"app-shop:8080"}]}`))
	if auth {
		t.Error("plain reverse_proxy reported basic auth")
	}
	if len(ups) != 1 || ups[0] != "app-shop:8080" {
		t.Errorf("upstreams = %v", ups)
	}
}

func TestInspectHandlerFindsAuthInsideSubroute(t *testing.T) {
	// This is the shape buildRouteJSON emits for basic_auth: the reverse_proxy
	// is nested inside a subroute behind an authentication handler, which is why
	// a dial-only PATCH cannot add or remove auth.
	subroute := []byte(`{
		"handler":"subroute",
		"routes":[
			{"match":[{"path":["/api/*"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"app-shop:8080"}]}]},
			{"handle":[
				{"handler":"authentication","providers":{"http_basic":{"accounts":[{"username":"admin","password":"$2a$14$x"}]}}},
				{"handler":"reverse_proxy","upstreams":[{"dial":"app-shop:8080"}]}
			]}
		]
	}`)

	auth, ups := inspectHandler(subroute)
	if !auth {
		t.Error("basic auth inside a subroute was not detected")
	}
	if len(ups) != 2 {
		t.Errorf("expected upstreams from both subroutes, got %v", ups)
	}
}

func TestInspectHandlerIgnoresOtherProviders(t *testing.T) {
	auth, _ := inspectHandler([]byte(`{"handler":"authentication","providers":{"other":{}}}`))
	if auth {
		t.Error("non-basic auth provider reported as basic auth")
	}
}

func TestInspectHandlerGarbage(t *testing.T) {
	auth, ups := inspectHandler([]byte(`not json`))
	if auth || ups != nil {
		t.Errorf("garbage handler returned (%v, %v)", auth, ups)
	}
}

func TestBuildRouteJSONBasicAuthShape(t *testing.T) {
	// Guards the assumption the reload/routes commands rely on: with auth, the
	// route's first handler is a subroute, not the reverse_proxy.
	data, err := buildRouteJSON("app-shop", []string{"shop.io"}, []string{"app-shop:8080"}, RouteOptions{
		BasicAuth: &BasicAuthConfig{Username: "admin", Password: "secret", BypassPaths: []string{"/api/*"}},
	})
	if err != nil {
		t.Fatalf("buildRouteJSON: %v", err)
	}

	var route struct {
		Handle []json.RawMessage `json:"handle"`
	}
	if err := json.Unmarshal(data, &route); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(route.Handle) != 1 {
		t.Fatalf("expected a single top-level handler, got %d", len(route.Handle))
	}
	auth, ups := inspectHandler(route.Handle[0])
	if !auth {
		t.Error("round trip lost the basic auth handler")
	}
	if len(ups) == 0 {
		t.Error("round trip lost the upstreams")
	}

	// The plaintext password must never reach Caddy.
	if strings.Contains(string(data), "secret") {
		t.Error("password was sent in plaintext instead of a bcrypt hash")
	}
}

// The 409 that broke `neo domain`. Caddy answers PUT-on-an-existing-key with
// `{"error":"key already exists: srv0"}`, and the old code ran every admin call
// through `curl -sf`, which discards the body and reports only a non-zero exit —
// so this reason was invisible outside the Caddy container's own log.
func TestParseAdminResponseSurfacesCaddyError(t *testing.T) {
	err := parseAdminResponse("PUT", CaddyAdminURL+"/config/apps/http/servers/srv0",
		"{\"error\":\"[/config/apps/http/servers/srv0] key already exists: srv0\"}\n409")
	if err == nil {
		t.Fatal("expected an error for a 409 response")
	}
	if !strings.Contains(err.Error(), "key already exists: srv0") {
		t.Errorf("error must carry Caddy's message, got %q", err)
	}
	if !strings.Contains(err.Error(), "409") {
		t.Errorf("error must carry the status code, got %q", err)
	}
	// The admin base URL is noise in every message; the config path is the signal.
	if strings.Contains(err.Error(), CaddyAdminURL) {
		t.Errorf("error should report the config path, not the full admin URL: %q", err)
	}
}

func TestParseAdminResponseAcceptsSuccess(t *testing.T) {
	for _, status := range []string{"200", "201", "204"} {
		if err := parseAdminResponse("PATCH", CaddyAdminURL+"/config/apps/tls", "\n"+status); err != nil {
			t.Errorf("status %s should succeed, got %v", status, err)
		}
	}
}

// A body-less failure still has to report the status rather than silently pass.
func TestParseAdminResponseHandlesEmptyBody(t *testing.T) {
	err := parseAdminResponse("POST", CaddyAdminURL+"/config/apps/http/servers/srv0/routes", "\n500")
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected the status in the error, got %q", err)
	}
}
