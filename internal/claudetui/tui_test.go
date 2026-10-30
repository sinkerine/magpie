//go:build darwin || linux

package claudetui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The test binary is also the shim (MAGPIE_TUI_TEST=shim) and a fake
// Claude Code (MAGPIE_TUI_TEST=claude): interactive, it shows a prompt,
// writes each line typed into its transcript as a turn answered "echo:
// <line>"; with -p it answers each stream-json line with a -p result.
func TestMain(m *testing.M) {
	switch os.Getenv("MAGPIE_TUI_TEST") {
	case "shim":
		if err := Run(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	case "claude":
		fakeClaude(os.Args[1:])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeClaude(args []string) {
	if slices.Contains(args, "-p") {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			fmt.Printf(`{"type":"result","subtype":"success","is_error":false,"result":"print mode"}` + "\n")
		}
		return
	}
	if os.Getenv("FAKE_FAIL") == "1" {
		fmt.Print("something went wrong\n")
		os.Exit(3)
	}
	sid := ""
	if i := slices.Index(args, "--session-id"); i >= 0 {
		sid = args[i+1]
	}
	dir := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "work")
	_ = os.MkdirAll(dir, 0o700)
	if os.Getenv("FAKE_TRUST") == "1" {
		fmt.Print("Do you trust this folder?\r\n ❯ Yes, I trust this folder\r\n   No, exit\r\n")
		r := bufio.NewReader(os.Stdin)
		if _, err := r.ReadString('\n'); err != nil {
			return
		}
	}
	// what it was started with, for the test to read
	_ = os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "args"), []byte(strings.Join(args, "\n")), 0o600)
	// raw, as Claude Code's terminal is: a paste is taken whole, and Enter
	// outside one sends it
	if tio, err := unix.IoctlGetTermios(0, ioctlGetTermios); err == nil {
		tio.Lflag &^= unix.ECHO | unix.ICANON
		tio.Iflag &^= unix.ICRNL
		_ = unix.IoctlSetTermios(0, ioctlSetTermios, tio)
	}
	fmt.Print("\r\n❯ ")
	r := bufio.NewReader(os.Stdin)
	var typed strings.Builder
	pasting := false
	for {
		c, err := r.ReadByte()
		if err != nil {
			return
		}
		switch {
		case c == 0x1b:
			seq := make([]byte, 5)
			if _, err := io.ReadFull(r, seq); err != nil {
				return
			}
			pasting = string(seq) == "[200~"
			continue
		case c != '\r' || pasting:
			typed.WriteByte(c)
			fmt.Printf("%c", c) // drawn in the prompt
			continue
		}
		line := typed.String()
		typed.Reset()
		f, _ := os.OpenFile(filepath.Join(dir, sid+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		rows := []any{
			map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": line}},
			map[string]any{"type": "assistant", "requestId": "req_1", "message": map[string]any{"id": "msg_1", "model": "claude-haiku", "role": "assistant",
				"content": []any{map[string]any{"type": "text", "text": "echo: " + line}}}},
			map[string]any{"type": "system", "subtype": "turn_duration"},
		}
		for _, row := range rows {
			b, _ := json.Marshal(row)
			_, _ = f.Write(append(b, '\n'))
		}
		f.Close()
		fmt.Print("\r\n❯ ")
	}
}

// shim runs the shim on the fake Claude Code with pargs, says lines on its
// input and returns what it wrote, once it wrote want lines.
func runShim(t *testing.T, env []string, pargs []string, lines []string, want int) []map[string]any {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cfg := filepath.Join(home, "claude")
	cmd := exec.Command(exe, pargs...)
	cmd.Env = append(os.Environ(), "MAGPIE_TUI_TEST=shim", "HOME="+home, "XDG_CACHE_HOME="+filepath.Join(home, "cache"), "CLAUDE_CONFIG_DIR="+cfg)
	cmd.Env = append(cmd.Env, env...)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	t.Setenv("MAGPIE_TUI_TEST_CFG", cfg)
	for _, l := range lines {
		b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": l}})
		_, _ = stdin.Write(append(b, '\n'))
	}
	got := make(chan map[string]any)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			var m map[string]any
			_ = json.Unmarshal(sc.Bytes(), &m)
			got <- m
		}
	}()
	var out []map[string]any
	by := time.After(30 * time.Second)
	for len(out) < want {
		select {
		case m := <-got:
			out = append(out, m)
		case <-by:
			t.Fatalf("the shim wrote %d lines of %d in 30s: %v", len(out), want, out)
		}
	}
	return out
}

func fakePath(t *testing.T) string {
	t.Helper()
	exe, _ := os.Executable()
	// the fake is the test binary, told so by its environment: a script
	// beside it starts it so
	p := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nMAGPIE_TUI_TEST=claude exec '"+exe+"' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

var pArgs = []string{"-p", "--output-format", "stream-json", "--input-format", "stream-json", "--include-partial-messages", "--verbose",
	"--model", "haiku", "--tools", "WebSearch", "--strict-mcp-config", "--mcp-config", "{}", "--setting-sources", "",
	"--dangerously-skip-permissions", "--effort", "high", "--thinking-display", "summarized"}

func TestShimTurnsInteractive(t *testing.T) {
	out := runShim(t, nil, append([]string{fakePath(t)}, pArgs...), []string{"hello", "/help"}, 5)
	if out[0]["type"] != "system" || out[0]["subtype"] != "init" || out[0]["session_id"] == "" {
		t.Fatalf("first line %v, want the init", out[0])
	}
	var texts []string
	results := 0
	for _, m := range out[1:] {
		switch m["type"] {
		case "assistant":
			b, _ := json.Marshal(m["message"])
			texts = append(texts, string(b))
		case "result":
			results++
		}
	}
	if len(texts) != 2 || !strings.Contains(texts[0], "echo: hello") || results != 2 {
		t.Fatalf("got %v", out)
	}
	// a slash opening the message is pasted after a line break: words,
	// not a command
	if !strings.Contains(texts[1], `\n/help"`) {
		t.Fatalf("the second turn said %s", texts[1])
	}
	args, _ := os.ReadFile(filepath.Join(os.Getenv("MAGPIE_TUI_TEST_CFG"), "args"))
	got := strings.Split(string(args), "\n")
	for _, no := range []string{"-p", "--dangerously-skip-permissions", "--output-format", "--verbose", "--thinking-display"} {
		if slices.Contains(got, no) {
			t.Errorf("the interactive Claude Code was given %s: %q", no, got)
		}
	}
	for _, yes := range []string{"--no-chrome", "mcp__magpie,WebSearch", "--session-id", "high"} {
		if !slices.Contains(got, yes) {
			t.Errorf("the interactive Claude Code wasn't given %s: %q", yes, got)
		}
	}
}

func TestShimFallsBackToPrint(t *testing.T) {
	for _, c := range []struct {
		name   string
		env    []string
		broken bool
	}{
		{"failed start", []string{"FAKE_FAIL=1"}, true},
		{"untrusted folder", []string{"FAKE_TRUST=1"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := runShim(t, c.env, append([]string{fakePath(t)}, pArgs...), []string{"hello"}, 1)
			if out[0]["result"] != "print mode" {
				t.Fatalf("got %v, want the -p run's answer", out)
			}
			home := filepath.Dir(os.Getenv("MAGPIE_TUI_TEST_CFG"))
			m, _ := filepath.Glob(filepath.Join(home, "*", "*", "magpie", "claude-tui-broken"))
			m2, _ := filepath.Glob(filepath.Join(home, "*", "magpie", "claude-tui-broken"))
			if got := len(m)+len(m2) > 0; got != c.broken {
				t.Fatalf("marked broken: %v, want %v", got, c.broken)
			}
		})
	}
}

func TestShimTrustsWhenTold(t *testing.T) {
	out := runShim(t, []string{"FAKE_TRUST=1", TrustEnv + "=1"}, append([]string{fakePath(t)}, pArgs...), []string{"hello"}, 3)
	if out[0]["subtype"] != "init" || out[1]["type"] != "assistant" {
		t.Fatalf("got %v, want an interactive turn", out)
	}
}

func TestPasteable(t *testing.T) {
	for _, c := range []struct {
		in   string
		busy bool
		want string
	}{
		{"hello", false, "hello"},
		{"/help", false, "\n/help"},
		{"  !ls", false, "\n!ls"},
		{"# note", true, userMark + "# note"},
		{"a\r\nb\x1b[201~c", false, "a\nb[201~c"},
		{"x / y", true, "x / y"},
	} {
		if got := pasteable(c.in, c.busy); got != c.want {
			t.Errorf("pasteable(%q, %v) = %q, want %q", c.in, c.busy, got, c.want)
		}
	}
}

type lines struct {
	out          []map[string]any
	rows, overed int
}

func (l *lines) emit(v any) {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	l.out = append(l.out, m)
}
func (l *lines) sawRow()   { l.rows++ }
func (l *lines) turnOver() { l.overed++ }

func TestTranslatorAPIError(t *testing.T) {
	l := &lines{}
	tr := newTranslator(l)
	for _, row := range []string{
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","isApiErrorMessage":true,"apiErrorStatus":429,"apiErrorParams":{"rate_limit_info":{"status":"rejected"}},"message":{"id":"x","model":"<synthetic>","content":[{"type":"text","text":"You've hit your limit"}]}}`,
		`{"type":"system","subtype":"turn_duration"}`,
		`{"type":"user","isSidechain":true,"message":{"role":"user","content":"agent"}}`,
	} {
		tr.row([]byte(row))
	}
	kinds := []string{}
	for _, m := range l.out {
		kinds = append(kinds, m["type"].(string))
	}
	if !slices.Equal(kinds, []string{"assistant", "rate_limit_event", "result"}) || l.rows != 1 || l.overed != 2 {
		t.Fatalf("got %v (rows %d, over %d)", l.out, l.rows, l.overed)
	}
	if r := l.out[2]; r["is_error"] != true || r["result"] != "You've hit your limit" || r["api_error_status"] != float64(429) {
		t.Fatalf("result %v", r)
	}
}
