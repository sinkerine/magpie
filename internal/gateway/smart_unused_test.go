package gateway

import (
	"net/http"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// Smart groups must use the windows of each account and each requested model,
// including the five-hour cap, rather than just the weekly percentage.
func TestSmartUnusedAccountWindows(t *testing.T) {
	fresh(t)
	old := allowances
	t.Cleanup(func() { allowances = old })
	now := time.Now()
	windows := map[string]provider.Allowance{
		"used":  {{Used: 5, Span: 5 * time.Hour, Resets: now.Add(time.Hour)}, {Used: 1, Span: 7 * 24 * time.Hour, Resets: now.Add(6 * 24 * time.Hour)}},
		"fresh": {{Used: 0, Span: 5 * time.Hour, Resets: now.Add(5 * time.Hour)}, {Used: 0, Span: 7 * 24 * time.Hour, Resets: now.Add(7 * 24 * time.Hour)}},
	}
	allowances = func(string) map[string]provider.Allowance { return windows }
	acct := func(user string) candidate {
		return candidate{p: provider.Provider{ID: "codex", Account: &provider.Account{Agent: "codex", User: user}}, model: "gpt-6.1-sol", rest: "codex@" + user}
	}
	cs := []candidate{acct("used"), acct("fresh")}
	p := provider.Provider{ID: "group/sol-fast"}
	check := func(want string) {
		t.Helper()
		got, _ := weigh(p, cs, "", provider.Chat)
		if got[0].p.Account.User != want {
			t.Fatalf("Smart picked %s, want %s", got[0].p.Account.User, want)
		}
	}
	check("fresh")
	servedCandidate(cs[1], 1000)
	check("fresh") // still unused by quota, ahead of the used account
	// A nonzero five-hour window prevents the special case even with a zero week.
	windows["fresh"][0].Used = 2
	check("used")
	windows["fresh"][0].Used = 0
	// A window for another model does not consume this model's allowance.
	windows["fresh"] = append(windows["fresh"], provider.Limit{Used: 99, Model: "opus", Span: 7 * 24 * time.Hour, Resets: now.Add(7 * 24 * time.Hour)})
	check("fresh")
	// A quota reading with no windows for this model is not an unused allowance.
	windows["fresh"] = provider.Allowance{windows["fresh"][2]}
	check("used")
	// Empty readings likewise receive no preference.
	windows["fresh"] = nil
	check("used")
	// Auto affinity still keeps an active turn on its original, used account.
	windows["fresh"] = provider.Allowance{{Span: 7 * 24 * time.Hour, Resets: now.Add(7 * 24 * time.Hour)}}
	got, wg := weigh(p, cs, "", provider.Chat)
	pl := planned{}
	for _, c := range got {
		pl.order = append(pl.order, weighed(c, p, wg, false, provider.Chat))
	}
	h := http.Header{}
	h.Set("X-Magpie-Session", "smart-unused-affinity")
	body := []byte(`{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"check","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"done"}]}`)
	key := p.ID + "|" + conversationID(h, body)
	sticks.Lock()
	sticks.m[key] = stick{who: cs[0].who(), rest: cs[0].rest, model: cs[0].model, turn: 1, at: now}
	sticks.Unlock()
	got, _, a, _ := affine(p.ID, "", false, h, provider.Chat, body, got, pl)
	if !a.Kept || a.Why != "turn" || got[0].p.Account.User != "used" {
		t.Fatalf("active turn lost affinity: %+v, first %s", a, got[0].p.Account.User)
	}
}
