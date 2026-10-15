package gateway

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/yetone/magpie/internal/provider"
)

// advisorTurn is a Claude Code turn with its advisor on, the tool as
// Claude Code 2.1.299 declares it (read from its request to a fake
// ANTHROPIC_BASE_URL, --advisor opus with the opus tier on magpie's id).
func advisorTurn(model, advisor string) string {
	return `{"model":"` + model + `","max_tokens":64,"stream":true,` +
		`"messages":[{"role":"user","content":"hi"}],` +
		`"tools":[{"name":"Read","description":"read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}},` +
		`{"type":"advisor_20260301","name":"advisor","model":"` + advisor + `","defer_loading":true}]}`
}

// Claude Code's advisor names its model as Claude Code names its own: on
// magpie, by magpie's id, which the vendor doesn't know, so the request was
// turned away whole and the advisor never ran (JackChou on Discord). The
// advisor's model now goes as the upstream's own name for it when it is
// the same provider's Claude model; one the upstream can't run (another
// provider's, a vendor's model asked) is left out and the turn still
// answered.
func TestAdvisorModelAsTheUpstreamNamesIt(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	u, url := newBillingUpstream(t)
	for _, p := range []provider.Provider{
		{ID: "relay", Name: "Relay", Key: "k", Anthropic: url, Models: []string{"claude-opus-5-5", "claude-sonnet-5-5"}},
		{ID: "other", Name: "Other", Key: "k", Anthropic: url, Models: []string{"claude-opus-5-5"}},
		{ID: "ds", Name: "DeepSeek", Key: "k", Anthropic: url, Models: []string{"deepseek-v4.1-flash"}},
	} {
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	advisors := func(b string) []string {
		var out []string
		for _, t := range gjson.Get(b, "tools").Array() {
			if strings.HasPrefix(t.Get("type").String(), "advisor_") {
				out = append(out, t.Get("model").String())
			}
		}
		return out
	}
	for _, c := range []struct {
		name, model, advisor, want string // want "" = left out
	}{
		{"same provider", "relay/claude-sonnet-5-5", "relay/claude-opus-5-5", "claude-opus-5-5"},
		{"1M mark", "relay/claude-sonnet-5-5", "relay/claude-opus-5-5[1m]", "claude-opus-5-5"},
		{"Claude's own id", "relay/claude-sonnet-5-5", "claude-opus-5-5", "claude-opus-5-5"},
		{"another provider", "relay/claude-sonnet-5-5", "other/claude-opus-5-5", ""},
		{"a vendor's model", "ds/deepseek-v4.1-flash", "relay/claude-opus-5-5", ""},
	} {
		claudeCodeSends(t, advisorTurn(c.model, c.advisor))
		b := u.last(t)
		got := advisors(b)
		switch {
		case c.want == "" && len(got) != 0, c.want != "" && (len(got) != 1 || got[0] != c.want):
			t.Errorf("%s: advisor sent as %q, want %q: %s", c.name, got, c.want, b)
		}
		if !strings.Contains(b, `"name":"Read"`) {
			t.Errorf("%s: the agent's own tool lost: %s", c.name, b)
		}
	}
}
