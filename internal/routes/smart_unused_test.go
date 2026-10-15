package routes

import (
	"slices"
	"testing"
	"time"
)

func TestSmartPrefersUnusedAccounts(t *testing.T) {
	now := time.Now()
	soon := []time.Time{now.Add(time.Hour)}
	later := []time.Time{now.Add(7 * 24 * time.Hour)}
	s := []Seat{
		{Key: "used", Account: true, Known: true, Used: 5, Soon: soon},
		{Key: "rounded", Account: true, Known: true, Soon: soon, Tokens: 100},
		{Key: "fresh", Account: true, Known: true, Soon: later},
		{Key: "unknown", Account: true},
		{Key: "learns", Account: true, Learns: true},
	}
	want := []string{"learns", "fresh", "rounded", "used", "unknown"}
	if got := keys(s, Apply(Smart, "p", s)); !slices.Equal(got, want) {
		t.Fatalf("unused accounts before used, by recent tokens: %v, want %v", got, want)
	}
	// Equal recent usage still goes by renewal, then the listed order.
	s[1].Tokens = 0
	want = []string{"learns", "rounded", "fresh", "used", "unknown"}
	if got := keys(s, Apply(Smart, "p", s)); !slices.Equal(got, want) {
		t.Fatalf("unused renewal tie: %v, want %v", got, want)
	}
}

func TestSmartUnusedPreferenceRequiresAccountWindows(t *testing.T) {
	now := time.Now()
	for _, other := range []Seat{
		{Key: "unknown", Account: true},
		{Key: "no-model-window", Account: true, Known: true},
		{Key: "key", Known: true, Soon: []time.Time{now.Add(7 * 24 * time.Hour)}},
	} {
		s := []Seat{other, {Key: "used", Account: true, Known: true, Used: 10, Soon: []time.Time{now.Add(time.Hour)}}}
		if got := keys(s, Apply(Smart, "p", s)); !slices.Equal(got, []string{"used", other.Key}) {
			t.Errorf("%s received unused preference: %v", other.Key, got)
		}
	}
}
