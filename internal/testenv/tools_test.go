package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFixtureToolsWithRestrictedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "input's text")
	if err := os.WriteFile(input, []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	program := filepath.Join(dir, "read")
	Program(t, program, Tools(t, "#!/bin/sh\n{{cat}} \"$1\"\n"))
	if out, err := exec.Command(program, input).CombinedOutput(); err != nil || string(out) != "fixture\n" {
		t.Fatalf("fixture with no tools on PATH: %q, %v", out, err)
	}
}
