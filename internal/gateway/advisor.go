package gateway

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/yetone/magpie/internal/provider"
)

// advisorTools fits Claude Code's advisor (/advisor, --advisor) to the
// upstream a Messages request goes to as it is (passthrough). Claude Code
// declares it as Anthropic's server tool, {"type": "advisor_20260301",
// "name": "advisor", "model": …}, and names the advisor's model as it
// names its own: on magpie, by magpie's id ("anthropic/claude-opus-5-5",
// or the tier an alias like opus is on), which no vendor knows, so the
// whole request was turned away (JackChou on Discord).
//
// The advisor runs at the vendor, on the same account: its model goes as
// that upstream's own name for it when it is p's, as the request's own
// model does (wires). An advisor magpie can't give that upstream (another
// provider's model, or not a Claude model) is left out, and so is every
// advisor when the request's model is not Claude's (a vendor's Messages
// API knows no such tool): the turn is answered without one, as a model
// that isn't on magpie would be.
func advisorTools(body []byte, p provider.Provider, upstream string, wires map[string]string) []byte {
	if !bytes.Contains(body, []byte(`"advisor_`)) {
		return body
	}
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return body
	}
	var top map[string]json.RawMessage
	var list []map[string]json.RawMessage
	if json.Unmarshal(body, &top) != nil || json.Unmarshal([]byte(tools.Raw), &list) != nil {
		return body
	}
	claude := anthropicModel.MatchString(upstream)
	changed := false
	kept := list[:0]
	for _, t := range list {
		var typ, model string
		json.Unmarshal(t["type"], &typ)
		if !strings.HasPrefix(typ, "advisor_") {
			kept = append(kept, t)
			continue
		}
		json.Unmarshal(t["model"], &model)
		name, ok := advisorModel(p, model, wires)
		if !claude || !ok {
			changed = true
			continue
		}
		if name != model {
			t["model"], _ = json.Marshal(name)
			changed = true
		}
		kept = append(kept, t)
	}
	if !changed {
		return body
	}
	if len(kept) == 0 {
		delete(top, "tools")
		delete(top, "tool_choice")
	} else {
		top["tools"], _ = json.Marshal(kept)
	}
	out, err := json.Marshal(top)
	if err != nil {
		return body
	}
	return out
}

// advisorModel is the name p's upstream knows the advisor model by, and
// whether p can run it: one of p's models, by magpie's id, or a Claude
// model's own id that names no provider.
func advisorModel(p provider.Provider, model string, wires map[string]string) (string, bool) {
	model = strings.TrimSuffix(strings.TrimSpace(model), "[1m]")
	if model == "" {
		return "", false
	}
	if q, m, ok := provider.Resolve(model); ok && !strings.HasPrefix(model, provider.GroupPrefix) {
		if q.ID != p.ID {
			return "", false
		}
		name := provider.UpstreamNameIn(wires, p.ID, m)
		return name, anthropicModel.MatchString(name)
	}
	if strings.Contains(model, "/") {
		return "", false
	}
	return model, anthropicModel.MatchString(model)
}
