package gateway

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
)

func TestGroupUltrafastWithoutOffer(t *testing.T) {
	tried := tierAccounts(t, nil, nil)
	files, err := filepath.Glob(filepath.Join(filepath.Dir(catalog.LivePath("codex")), "codex@*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("account model lists: %v, %v", files, err)
	}
	for _, path := range files {
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		models, _, ok := catalog.Live(id)
		if !ok {
			t.Fatal("missing cached account models")
		}
		for i := range models {
			models[i].Tiers = []string{"priority"}
		}
		if err := catalog.SaveLive(id, provider.CodexBase, models); err != nil {
			t.Fatal(err)
		}
	}
	provider.Changed()
	if provider.UltraModels()["gpt-6-luna"] {
		t.Fatal("fixture still advertises Ultrafast")
	}
	if err := provider.SaveGroup(provider.Group{ID: "luna-ultrafast", Members: []string{"codex/gpt-6-luna"}, Ultra: []string{"codex/gpt-6-luna"}}); err != nil {
		t.Fatal(err)
	}
	to, _ := url.Parse(provider.CodexBase)
	provider.CodexBase = "https://chatgpt.com/backend-api/codex"
	provider.Changed()
	s := New()
	s.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = to.Scheme, to.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
	postFrom(t, s, "opencode", "/v1/responses", `{"model":"group/luna-ultrafast","input":"ping"}`)
	if got := strings.Join(tried(), ","); got != "acct-plus:priority" {
		t.Fatalf("no eligible account: tried %s, want acct-plus:priority", got)
	}
}
