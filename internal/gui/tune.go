package gui

// The Context tab's advice: for each agent, the settings that would have
// cost this user the fewest tokens over the period, worked out by
// replaying their own calls (internal/tune) against what the agent's
// config has now, and putting one in, or back, on their say.

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/sessions"
	"github.com/yetone/magpie/internal/tune"
)

type tuneAdvice struct {
	Knob    string            `json:"knob"`
	Model   string            `json:"model,omitempty"`
	Setting agent.TuneSetting `json:"setting"`
	Compact *tune.Compaction  `json:"compact,omitempty"`
	TTL     *tune.TTL         `json:"ttl,omitempty"`
}

type tuneAgent struct {
	Agent  string       `json:"agent"`
	Calls  int          `json:"calls"`
	Advice []tuneAdvice `json:"advice"`
}

type tuneState struct {
	Days   int         `json:"days"`
	Agents []tuneAgent `json:"agents"`
}

// tuneOf works the advice out of the calls the agents' files hold.
func tuneOf(cs []sessions.Call, days int, home string) tuneState {
	by := map[string][]tune.Call{}
	for _, c := range cs {
		if c.Agent == "claude" || c.Agent == "codex" {
			by[c.Agent] = append(by[c.Agent], tune.FromCalls([]sessions.Call{c})...)
		}
	}
	out := tuneState{Days: days, Agents: []tuneAgent{}}
	for _, name := range []string{"claude", "codex"} {
		calls := by[name]
		if len(calls) == 0 {
			continue
		}
		ta := tuneAgent{Agent: name, Calls: len(calls), Advice: []tuneAdvice{}}
		switch name {
		case "claude":
			models := map[string][]tune.Call{}
			var main, side []tune.Call
			for _, c := range calls {
				models[c.Model] = append(models[c.Model], c)
				if c.Sub {
					side = append(side, c)
				} else {
					main = append(main, c)
				}
			}
			for m, mc := range models {
				s, err := agent.TuneRead(home, name, agent.TuneCompact, m)
				if err != nil {
					continue
				}
				// a model asked for with [1m], or seen past 200K, has the
				// whole 1M; another the 200K Claude Code's own models start with
				window := 200000
				for _, c := range mc {
					if c.Long || c.Prompt > window {
						window = 1000000
						break
					}
				}
				cur, _ := strconv.Atoi(s.Value)
				o := tune.CompactOpts{Min: 100000, Max: 1000000, Step: 10000, Reserve: 33000, Window: window, Current: cur}
				if r := tune.Compact(mc, o); r != nil {
					ta.Advice = append(ta.Advice, tuneAdvice{Knob: agent.TuneCompact, Model: m, Setting: s, Compact: r})
				}
			}
			sort.SliceStable(ta.Advice, func(i, j int) bool {
				return ta.Advice[i].Compact.Facts.Calls > ta.Advice[j].Compact.Facts.Calls
			})
			for _, k := range []struct {
				knob  string
				calls []tune.Call
			}{{agent.TuneCache, main}, {agent.TuneSubCache, side}} {
				s, err := agent.TuneRead(home, name, k.knob, "")
				if err != nil {
					continue
				}
				if r := tune.CacheTTL(k.calls, s.Value); r != nil {
					ta.Advice = append(ta.Advice, tuneAdvice{Knob: k.knob, Setting: s, TTL: r})
				}
			}
		case "codex":
			s, err := agent.TuneRead(home, name, agent.TuneCompact, "")
			if err != nil {
				continue
			}
			window := 272000
			for _, c := range calls {
				window = max(window, c.Prompt)
			}
			cur, _ := strconv.Atoi(s.Value)
			o := tune.CompactOpts{Min: 50000, Max: 1000000, Step: 10000, Window: window, Current: cur}
			if r := tune.Compact(calls, o); r != nil {
				ta.Advice = append(ta.Advice, tuneAdvice{Knob: agent.TuneCompact, Setting: s, Compact: r})
			}
		}
		out.Agents = append(out.Agents, ta)
	}
	return out
}

func tuneRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tune", func(rw http.ResponseWriter, r *http.Request) {
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		if days <= 0 || days > 30 {
			days = 7
		}
		since := time.Now().AddDate(0, 0, -days)
		writeJSON(rw, tuneOf(sessions.Calls(since), days, ""))
	})
	type change struct {
		Agent, Knob, Model, Value string
	}
	mux.HandleFunc("POST /api/tune/apply", func(rw http.ResponseWriter, r *http.Request) {
		var in change
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		if err := agent.TuneApply("", in.Agent, in.Knob, in.Model, in.Value); err != nil {
			fail(rw, err)
			return
		}
		s, _ := agent.TuneRead("", in.Agent, in.Knob, in.Model)
		writeJSON(rw, s)
	})
	mux.HandleFunc("POST /api/tune/undo", func(rw http.ResponseWriter, r *http.Request) {
		var in change
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		if err := agent.TuneUndo("", in.Agent, in.Knob, in.Model); err != nil {
			fail(rw, err)
			return
		}
		s, _ := agent.TuneRead("", in.Agent, in.Knob, in.Model)
		writeJSON(rw, s)
	})
}
