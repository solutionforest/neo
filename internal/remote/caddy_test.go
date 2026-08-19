package remote

import (
	"encoding/json"
	"fmt"
	"os"
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

// The splitter takes everything after the LAST newline as the status line, so
// a pretty-printed (multi-line) JSON body must still parse correctly: the body
// stays intact, and the whole thing is searched for the `.error` message.
func TestParseAdminResponseMultilineBodyStatusIsLastLine(t *testing.T) {
	out := "{\n  \"error\": \"invalid traversal path\"\n}\n500"
	err := parseAdminResponse("PATCH", CaddyAdminURL+"/config/apps/tls/automation", out)
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	if !strings.Contains(err.Error(), "invalid traversal path") {
		t.Errorf("expected the multi-line body's error message to survive, got %q", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected the status code in the error, got %q", err)
	}
}

// A response with no newline anywhere — body and status glued together, an
// unexpected shape curl should never actually produce — must fail loudly
// rather than panic or misread the glued string as a status code.
func TestParseAdminResponseNoNewlineSeparator(t *testing.T) {
	err := parseAdminResponse("PUT", CaddyAdminURL+"/config/apps/http/servers/srv0", `{"error":"boom"}500`)
	if err == nil {
		t.Fatal("expected an error for a response with no newline separator")
	}
	if !strings.Contains(err.Error(), "unreadable response") {
		t.Errorf("expected an \"unreadable response\" style error, got %q", err)
	}
}

// Output with no parseable status code at all — curl printed nothing, or a
// connection-error string instead of a status line — must be a hard failure,
// never success. Treating this as success would reintroduce exactly the class
// of bug being fixed: a real failure disappearing behind a green result.
func TestParseAdminResponseUnparseableStatusIsNotSuccess(t *testing.T) {
	cases := map[string]string{
		"empty output":          "",
		"curl connection error": "curl: (7) Failed to connect to localhost port 2019: Connection refused",
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			err := parseAdminResponse("PATCH", CaddyAdminURL+"/config/apps/tls", out)
			if err == nil {
				t.Fatalf("%s must not be treated as success", name)
			}
			if !strings.Contains(err.Error(), "unreadable response") {
				t.Errorf("expected an \"unreadable response\" style error, got %q", err)
			}
		})
	}
}

func TestParseAdminResponseStatusBoundaries(t *testing.T) {
	cases := []struct {
		status    string
		wantError bool
	}{
		{"199", true},
		{"200", false},
		{"299", false},
		{"300", true},
	}
	for _, tc := range cases {
		err := parseAdminResponse("PATCH", CaddyAdminURL+"/config/apps/tls", "\n"+tc.status)
		if tc.wantError && err == nil {
			t.Errorf("status %s: expected an error, got nil", tc.status)
		}
		if !tc.wantError && err != nil {
			t.Errorf("status %s: expected success, got %v", tc.status, err)
		}
	}
}

func TestCaddyErrorMessageExtractsErrorField(t *testing.T) {
	got := caddyErrorMessage(`{"error":"key already exists: srv0"}`)
	if got != "key already exists: srv0" {
		t.Errorf("got %q", got)
	}
}

func TestCaddyErrorMessageFallsBackWhenNoErrorField(t *testing.T) {
	body := `{"foo":"bar"}`
	if got := caddyErrorMessage(body); got != body {
		t.Errorf("expected the raw JSON body back when there is no .error field, got %q", got)
	}
}

func TestCaddyErrorMessageFallsBackForNonJSONBody(t *testing.T) {
	body := "<html><body><h1>502 Bad Gateway</h1></body></html>"
	if got := caddyErrorMessage(body); got != body {
		t.Errorf("expected the raw non-JSON body back, got %q", got)
	}
}

func TestCaddyErrorMessageEmptyBody(t *testing.T) {
	if got := caddyErrorMessage(""); got != "" {
		t.Errorf("expected empty string for an empty body, got %q", got)
	}
}

func TestCaddyErrorMessageTruncatesLongBody(t *testing.T) {
	// Exactly 200 chars: must NOT be truncated (the check is `> 200`).
	exact := strings.Repeat("a", 200)
	if got := caddyErrorMessage(exact); got != exact {
		t.Errorf("a 200-char body must be returned unchanged, got len %d", len(got))
	}

	// 201 chars: truncated to the first 200 plus a literal "...".
	over := strings.Repeat("b", 201)
	got := caddyErrorMessage(over)
	if len(got) != 203 {
		t.Fatalf("expected truncated length 203 (200 + \"...\"), got %d (%q)", len(got), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected truncated body to end with \"...\", got %q", got)
	}
	if got[:200] != strings.Repeat("b", 200) {
		t.Errorf("expected the first 200 characters to be preserved exactly, got %q", got[:200])
	}
}

func TestAdminPathStripsPrefix(t *testing.T) {
	got := adminPath(CaddyAdminURL + "/config/apps/http/servers/srv0")
	if got != "/config/apps/http/servers/srv0" {
		t.Errorf("got %q", got)
	}
}

func TestAdminPathLeavesUnrelatedURLUnchanged(t *testing.T) {
	url := "https://example.com/config/apps/http/servers/srv0"
	if got := adminPath(url); got != url {
		t.Errorf("expected a URL without the admin prefix to be returned unchanged, got %q", got)
	}
}

// fakeExecutor is a scriptable stand-in for *ssh.Executor. It implements only
// the caddyExecutor seam (Run, RunQuiet, WriteFileElevated, FileExists) — the
// four methods Caddy's admin-API helpers call directly. Everything else Caddy
// does goes through NewDocker(c.raw), which is deliberately left unseamed, so
// these tests construct a *Caddy literal (package-internal) rather than going
// through NewCaddy.
type fakeExecutor struct {
	responses []fakeResponse // consumed in order, one per call to Run
	calls     []string       // every command passed to Run, in call order
}

type fakeResponse struct {
	out string
	err error
}

func (f *fakeExecutor) Run(cmd string) (string, error) {
	f.calls = append(f.calls, cmd)
	if len(f.responses) == 0 {
		return "", fmt.Errorf("fakeExecutor: unexpected call (no scripted response left): %s", cmd)
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp.out, resp.err
}

func (f *fakeExecutor) RunQuiet(cmd string) error {
	_, err := f.Run(cmd)
	return err
}

func (f *fakeExecutor) WriteFileElevated(remotePath string, data []byte, mode os.FileMode) error {
	return nil
}

func (f *fakeExecutor) FileExists(path string) bool {
	return false
}

// This is the actual production bug: saveHTTPServerConfig used PUT on srv0,
// and srv0 is created by `neo init`, so PUT always found the key already
// present and got back 409 `key already exists: srv0` — AddRouteHTTP
// propagated that failure and `neo domain` aborted before the route was ever
// added. adminSet's PATCH-first strategy is what makes this succeed against a
// real server, and PUT must never even be attempted when PATCH works.
func TestAdminSetPatchesExistingKeyWithoutFallingBackToPUT(t *testing.T) {
	fake := &fakeExecutor{responses: []fakeResponse{
		{out: "\n200"}, // PATCH succeeds
	}}
	c := &Caddy{exec: fake}

	if err := c.adminSet(CaddyAdminURL+"/config/apps/http/servers/srv0", `{"listen":[":443",":80"],"routes":[]}`); err != nil {
		t.Fatalf("expected adminSet to succeed via PATCH, got %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one call (PATCH only), got %d: %v", len(fake.calls), fake.calls)
	}
	if !strings.Contains(fake.calls[0], "-X PATCH") {
		t.Errorf("expected the call to be a PATCH, got %q", fake.calls[0])
	}
}

func TestAdminSetFallsBackToPUTWhenPATCHFails(t *testing.T) {
	fake := &fakeExecutor{responses: []fakeResponse{
		{out: "{\"error\":\"invalid traversal path\"}\n500"}, // PATCH fails: key absent
		{out: "\n200"},                                       // PUT creates it
	}}
	c := &Caddy{exec: fake}

	if err := c.adminSet(CaddyAdminURL+"/config/apps/tls", "{}"); err != nil {
		t.Fatalf("expected adminSet to succeed via the PUT fallback, got %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected two calls (PATCH then PUT), got %d: %v", len(fake.calls), fake.calls)
	}
	if !strings.Contains(fake.calls[0], "-X PATCH") {
		t.Errorf("expected the first call to be PATCH, got %q", fake.calls[0])
	}
	if !strings.Contains(fake.calls[1], "-X PUT") {
		t.Errorf("expected the fallback call to be PUT, got %q", fake.calls[1])
	}
}

// When both verbs fail, adminSet must report the PATCH failure — the key
// already existing is the common case on a live server, so that error
// describes what actually went wrong far more often than the PUT one does.
func TestAdminSetReturnsPATCHErrorWhenBothFail(t *testing.T) {
	fake := &fakeExecutor{responses: []fakeResponse{
		{out: "{\"error\":\"invalid traversal path\"}\n500"},   // PATCH failure
		{out: "{\"error\":\"key already exists: srv0\"}\n409"}, // PUT failure
	}}
	c := &Caddy{exec: fake}

	err := c.adminSet(CaddyAdminURL+"/config/apps/http/servers/srv0", "{}")
	if err == nil {
		t.Fatal("expected an error when both PATCH and PUT fail")
	}
	// Pin the call order too: the error-content assertions below would pass
	// "by accident" if adminSet tried PUT before PATCH, since this fake
	// returns scripted responses in call order regardless of which verb asked
	// for them — a verb-order regression would silently swap which failure
	// each call receives without changing the returned message.
	if len(fake.calls) != 2 {
		t.Fatalf("expected exactly two calls (PATCH then PUT), got %d: %v", len(fake.calls), fake.calls)
	}
	if !strings.Contains(fake.calls[0], "-X PATCH") {
		t.Errorf("expected the first call to be PATCH, got %q", fake.calls[0])
	}
	if !strings.Contains(fake.calls[1], "-X PUT") {
		t.Errorf("expected the second call to be PUT, got %q", fake.calls[1])
	}
	if !strings.Contains(err.Error(), "invalid traversal path") {
		t.Errorf("expected the PATCH error to be the one surfaced, got %q", err)
	}
	if strings.Contains(err.Error(), "key already exists") {
		t.Errorf("PUT's error must not be the one surfaced, got %q", err)
	}
}

// Same flaw ensureTLSApp had: the guard this replaced tested curl's exit
// status, and Caddy answers HTTP 200 with a literal `null` body for a missing
// key — so the exit-status check saw "success" and skipped the create, and
// every later write failed with `invalid traversal path`. ensureHTTPServer
// must check the body, not the exit status.
func TestEnsureHTTPServerCreatesWhenBodyIsNull(t *testing.T) {
	fake := &fakeExecutor{responses: []fakeResponse{
		{out: "null"},  // GET srv0: absent (200 OK, body "null")
		{out: "\n200"}, // adminSet's PATCH create succeeds
	}}
	c := &Caddy{exec: fake}

	if err := c.ensureHTTPServer(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected a GET followed by a create call, got %d calls: %v", len(fake.calls), fake.calls)
	}
}

func TestEnsureHTTPServerNoopsWhenBodyIsPresent(t *testing.T) {
	fake := &fakeExecutor{responses: []fakeResponse{
		{out: `{"listen":[":443",":80"],"routes":[]}`},
	}}
	c := &Caddy{exec: fake}

	if err := c.ensureHTTPServer(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected only the GET check with no create call, got %d calls: %v", len(fake.calls), fake.calls)
	}
}

func TestEnsureTLSAppCreatesWhenBodyIsNull(t *testing.T) {
	fake := &fakeExecutor{responses: []fakeResponse{
		{out: "null"},
		{out: "\n200"},
	}}
	c := &Caddy{exec: fake}

	if err := c.ensureTLSApp(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected a GET followed by a create call, got %d calls: %v", len(fake.calls), fake.calls)
	}
}

func TestEnsureTLSAppNoopsWhenBodyIsPresent(t *testing.T) {
	fake := &fakeExecutor{responses: []fakeResponse{
		{out: `{"automation":{"policies":[]}}`},
	}}
	c := &Caddy{exec: fake}

	if err := c.ensureTLSApp(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected only the GET check with no create call, got %d calls: %v", len(fake.calls), fake.calls)
	}
}

// A GET failure (curl error, connection refused, etc.) must be treated the
// same as an absent key — err != nil skips the "present" short-circuit in
// both ensureHTTPServer and ensureTLSApp — rather than propagating and
// aborting before the create is attempted.
func TestEnsureHTTPServerCreatesWhenGetFails(t *testing.T) {
	fake := &fakeExecutor{responses: []fakeResponse{
		{out: "", err: fmt.Errorf("ssh run: curl: (7) Failed to connect")},
		{out: "\n200"},
	}}
	c := &Caddy{exec: fake}

	if err := c.ensureHTTPServer(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected a GET followed by a create call, got %d calls: %v", len(fake.calls), fake.calls)
	}
}
