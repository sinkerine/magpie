package cli

import (
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestGroupUltrafastPairs(t *testing.T) {
	groupsHome(t)
	const member = "codex/gpt-6-luna"
	g := provider.Group{ID: "luna-ultrafast", Members: []string{member}}
	resolve := func(id string) (string, error) { return id, nil }
	apply := func(pair string) {
		t.Helper()
		if err := applyGroupPairs(&g, []string{pair}, resolve, false); err != nil {
			t.Fatal(err)
		}
	}
	apply("ultrafast=" + member)
	if !slices.Equal(g.Ultra, []string{member}) {
		t.Fatalf("ultrafast=%v", g.Ultra)
	}
	apply("ultrafast=")
	if len(g.Ultra) != 0 {
		t.Fatal("empty ultrafast did not clear tier")
	}
	apply("ultrafast=" + member)
	apply("fast=" + member)
	if len(g.Ultra) != 0 || !g.IsFast(member) {
		t.Fatal("ordinary Fast kept Ultrafast")
	}
	apply("ultrafast=" + member)
	apply("fast=")
	if len(g.Ultra) != 0 || len(g.Fast) != 0 {
		t.Fatal("standard speed kept a fast tier")
	}
	if err := applyGroupPairs(&g, []string{"ultrafast=missing/model"}, resolve, false); err == nil {
		t.Fatal("accepted a non-member")
	}
}
