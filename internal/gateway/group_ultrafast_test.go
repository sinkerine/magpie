package gateway

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// A group's tier applies without a client tier or an agent fast pick,
// including when a parent group resolves it and when protocols change.
func TestGroupUltrafast(t *testing.T) {
	proOut := false
	tried := tierAccounts(t, &proOut, nil)
	to, _ := url.Parse(provider.CodexBase)
	provider.CodexBase = "https://chatgpt.com/backend-api/codex"
	s := New()
	s.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = to.Scheme, to.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
	var group provider.Group
	if err := json.Unmarshal([]byte(`{"id":"luna-ultrafast","members":["codex/gpt-6-luna"],"fast":["codex/gpt-6-luna"],"ultra":["codex/gpt-6-luna"]}`), &group); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(group); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{ID: "parent", Members: []string{"group/luna-ultrafast"}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{ID: "luna-fast", Members: []string{"codex/gpt-6-luna"}, Fast: []string{"codex/gpt-6-luna"}}); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"group/luna-ultrafast", "group/parent"} {
		for _, api := range []struct{ path, body string }{
			{"/v1/responses", `{"model":"MODEL","input":"ping"}`},
			{"/v1/chat/completions", `{"model":"MODEL","messages":[{"role":"user","content":"ping"}]}`},
			{"/v1/messages", `{"model":"MODEL","max_tokens":64,"messages":[{"role":"user","content":"ping"}]}`},
		} {
			r := postFrom(t, s, "opencode", api.path, strings.ReplaceAll(api.body, "MODEL", model))
			if len(r.Tries) == 0 {
				t.Fatalf("%s %s: no tries", model, api.path)
			}
			if got := strings.Join(tried(), ","); got != "acct-pro:ultrafast" {
				t.Errorf("%s %s tried %s, want acct-pro:ultrafast", model, api.path, got)
			}
		}
	}
	postFrom(t, s, "opencode", "/v1/responses", `{"model":"group/luna-fast","input":"ping"}`)
	if got := strings.Join(tried(), ","); got != "acct-plus:priority" {
		t.Errorf("ordinary Fast tried %s", got)
	}
	proOut = true
	postFrom(t, s, "opencode", "/v1/responses", `{"model":"group/luna-ultrafast","input":"ping"}`)
	if got := strings.Join(tried(), ","); got != "acct-pro:ultrafast,acct-plus:priority" {
		t.Errorf("fallback tried %s", got)
	}
}
