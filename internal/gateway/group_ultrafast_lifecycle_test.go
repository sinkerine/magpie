package gateway

import (
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestGroupUltrafastLifecycle(t *testing.T) {
	tierAccounts(t, nil, nil)
	const member = "codex/gpt-6-luna"
	original := provider.Group{ID: "ultra", Members: []string{member}, Ultra: []string{member, member, "gone/model"}}
	if err := provider.SaveGroup(original); err != nil {
		t.Fatal(err)
	}
	if len(original.Ultra) != 3 || len(original.Fast) != 0 {
		t.Fatal("save changed caller's lists")
	}
	check := func(id, want string) provider.Group {
		t.Helper()
		g, ms, ok := provider.FindGroup("group/" + id)
		if !ok || len(ms) != 1 || !ms[0].Fast || !ms[0].Ultra || !slices.Equal(g.Ultra, []string{want}) || !g.IsFast(want) {
			t.Fatalf("%s: group %+v, members %+v", id, g, ms)
		}
		return g
	}
	g := check("ultra", member)
	g.Ultra[0] = "changed/model"
	check("ultra", member)
	copied, err := provider.CopyGroup("ultra", "Ultra copy")
	if err != nil {
		t.Fatal(err)
	}
	check(copied.ID, member)
	shared, err := provider.ShareGroup("ultra")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := provider.ImportGroup(shared)
	if err != nil {
		t.Fatal(err)
	}
	check(imported.ID, member)
	g = check("ultra", member)
	g.RenameMember(member, member+":high")
	if err := provider.SaveGroup(g); err != nil {
		t.Fatal(err)
	}
	g = check("ultra", member+":high")
	g.SetMemberFast(member+":high", false)
	if err := provider.SaveGroup(g); err != nil {
		t.Fatal(err)
	}
	g, ms, _ := provider.FindGroup("group/ultra")
	if len(g.Ultra) != 0 || len(g.Fast) != 0 || ms[0].Ultra || ms[0].Fast {
		t.Fatal("switching fast off kept UltraFast")
	}
	for _, invalid := range []provider.Group{
		{ID: "bad", Members: []string{"group/ultra"}, Ultra: []string{"group/ultra"}},
		{ID: "bad", Members: []string{member}, Ultra: []string{member + ":high"}},
	} {
		// A tier naming a removed member is pruned, while a tier on a nested group is refused.
		err := provider.SaveGroup(invalid)
		if invalid.Members[0] == "group/ultra" && err == nil {
			t.Fatal("accepted UltraFast on a group")
		}
		if invalid.Members[0] == member && err != nil {
			t.Fatal(err)
		}
	}
}
