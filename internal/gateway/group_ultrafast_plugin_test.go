package gateway

import (
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestGroupUltrafastPlugin(t *testing.T) {
	t.Setenv("FAKE_TIERS", "1")
	up := &tierUp{}
	pid := besideFake(t, "tiered", up)
	member := pid + "/fake-1"
	if err := provider.SaveGroup(provider.Group{ID: "ultra", Members: []string{member}, Ultra: []string{member}}); err != nil {
		t.Fatal(err)
	}
	s := New()
	for _, api := range []struct{ path, body string }{
		{"/v1/responses", `{"model":"group/ultra","input":"ping"}`},
		{"/v1/chat/completions", `{"model":"group/ultra","messages":[{"role":"user","content":"ping"}]}`},
		{"/v1/messages", `{"model":"group/ultra","max_tokens":64,"messages":[{"role":"user","content":"ping"}]}`},
	} {
		if r := postProto(t, s, api.path, api.body); len(r.Tries) != 1 {
			t.Fatalf("%s tries %+v", api.path, r.Tries)
		}
		if got := up.last(t); got != "ultrafast" {
			t.Errorf("%s sent %v, want ultrafast", api.path, got)
		}
	}
	for _, model := range []string{"fake-resp", "fake-claude"} {
		member := pid + "/" + model
		if err := provider.SaveGroup(provider.Group{ID: "bad", Members: []string{member}, Ultra: []string{member}}); err == nil {
			t.Errorf("accepted unsupported Ultrafast on %s", model)
		}
	}
}
