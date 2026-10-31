package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var toolPath = filepath.SplitList(os.Getenv("PATH"))

// Tool resolves a fixture's system tool from the test binary's initial
// PATH, before tests narrow it. The absolute path works on NixOS too.
func Tool(t testing.TB, name string) string {
	t.Helper()
	var p string
	for _, dir := range toolPath {
		if !filepath.IsAbs(dir) {
			continue
		}
		if found, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			p = found
			break
		}
	}
	if p == "" {
		t.Fatalf("fixture needs %s on the initial PATH", name)
	}
	p, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ToolDir makes a PATH directory containing only the tools a fixture needs.
func ToolDir(t testing.TB, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.Symlink(Tool(t, name), filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Tools expands {{name}} in a shell fixture to a quoted, absolute tool
// path. Call it before replacing PATH with the fixture's own commands.
func Tools(t testing.TB, script string) string {
	t.Helper()
	for {
		start := strings.Index(script, "{{")
		if start < 0 {
			return script
		}
		end := strings.Index(script[start+2:], "}}")
		if end < 0 {
			t.Fatal("unterminated fixture tool placeholder")
		}
		end += start + 2
		name := script[start+2 : end]
		path := "'" + strings.ReplaceAll(Tool(t, name), "'", "'\"'\"'") + "'"
		script = script[:start] + path + script[end+2:]
	}
}
