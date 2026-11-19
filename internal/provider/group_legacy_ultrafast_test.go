package provider

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestGroupReadsLegacyUltrafast(t *testing.T) {
	const member = "codex/gpt-6-luna"
	var f file
	if err := decodeProviderFile([]byte(`{"providers":[{"id":"relay"}],"noAutoGroups":true,"groups":[{"id":"luna","members":["codex/gpt-6-luna"],"ultrafast":["codex/gpt-6-luna"]}]}`), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Groups) != 1 || len(f.Providers) != 1 || f.Providers[0].ID != "relay" || !f.NoAutoGroups {
		t.Fatalf("provider file lost fields: %+v", f)
	}
	g := f.Groups[0]
	if !g.IsUltra(member) || !slices.Equal(g.Fast, []string{member}) {
		t.Fatalf("legacy speed lost: fast=%v ultra=%v", g.Fast, g.Ultra)
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Groups []map[string]json.RawMessage `json:"groups"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if _, old := saved.Groups[0]["ultrafast"]; old || saved.Groups[0]["ultra"] == nil {
		t.Fatalf("saved speed is not canonical: %s", data)
	}
	if err := decodeProviderFile([]byte(`{"groups":[{"ultra":[],"ultrafast":["codex/gpt-6-luna"]}]}`), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Groups[0].Ultra) != 0 {
		t.Fatal("legacy setting overrode an explicit upstream setting")
	}
}

func TestGroupEmbeddedRequestKeepsFields(t *testing.T) {
	var req struct {
		Group
		From string `json:"from"`
	}
	if err := json.Unmarshal([]byte(`{"id":"new","from":"old","ultra":["codex/gpt-6-luna"]}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.ID != "new" || req.From != "old" || !slices.Equal(req.Ultra, []string{"codex/gpt-6-luna"}) {
		t.Fatalf("embedded request lost fields: %+v", req)
	}
}
