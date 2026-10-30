package gateway

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/wslrun"
)

// A Claude run is Claude Code's interactive mode on macOS and Linux, for
// this machine's Claude Code, unless the user turned it off or the run
// needs what only -p takes.
func TestClaudeTUIChosen(t *testing.T) {
	was := settings.Load()
	t.Cleanup(func() { _ = settings.Save(was) })
	claudeTUIRuns = true
	t.Cleanup(func() { claudeTUIRuns = false })
	here := claudeCLI{path: "/usr/local/bin/claude"}
	text := func(s string) []Message {
		return []Message{{Role: "user", Parts: []Part{{Kind: Text, Text: s}}}}
	}
	prompt := []map[string]any{{"type": "text", "text": "hi"}}
	on := runtime.GOOS == "darwin" || runtime.GOOS == "linux"
	cases := []struct {
		name   string
		req    *Request
		binary claudeCLI
		prompt []map[string]any
		print  bool
		want   bool
	}{
		{"a plain turn", &Request{Messages: text("hi")}, here, prompt, false, on},
		{"turned off", &Request{Messages: text("hi")}, here, prompt, true, false},
		{"in WSL", &Request{Messages: text("hi")}, claudeCLI{path: "claude", wsl: &wslrun.Tool{}}, prompt, false, false},
		{"safeguards", &Request{Messages: text("hi"), Safeguards: json.RawMessage(`{"a":1}`)}, here, prompt, false, false},
		{"client cache marks", &Request{Messages: text("hi"), clientCache: &clientCache{}}, here, prompt, false, false},
		{"a schema", &Request{Messages: text("hi"), Format: &Format{Type: "json_schema", Schema: json.RawMessage(`{"type":"object"}`)}}, here, prompt, false, false},
		{"an image", &Request{Messages: []Message{{Role: "user", Parts: []Part{{Kind: Image, MediaType: "image/png", Data: "AA=="}}}}}, here, prompt, false, false},
		{"a long first message", &Request{Messages: text("hi")}, here, []map[string]any{{"type": "text", "text": strings.Repeat("x", tuiPromptMost)}}, false, false},
	}
	for _, c := range cases {
		st := settings.Load()
		st.ClaudePrintMode = c.print
		if err := settings.Save(st); err != nil {
			t.Fatal(err)
		}
		if got := claudeTUI(c.req, c.binary, c.prompt); got != c.want {
			t.Errorf("%s: claudeTUI = %v, want %v", c.name, got, c.want)
		}
	}
}
