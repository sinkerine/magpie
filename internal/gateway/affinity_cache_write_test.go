package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// cacheWriter is an Anthropic Messages upstream that answers as each key,
// reporting the cache as Anthropic does: a first turn writes the prompt
// (cache_creation_input_tokens) and reads none of it.
type cacheWriter struct {
	mu          sync.Mutex
	tried       []string
	read, wrote int
}

func (c *cacheWriter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	io.ReadAll(r.Body)
	key := r.Header.Get("x-api-key")
	if key == "" {
		key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	c.mu.Lock()
	c.tried = append(c.tried, key)
	read, wrote := c.read, c.wrote
	c.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	usage := `"cache_read_input_tokens":` + itoa(read) + `,"cache_creation_input_tokens":` + itoa(wrote)
	for _, ev := range []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"usage":{"input_tokens":12,"output_tokens":1,` + usage + `}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"from ` + key + `"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	} {
		var t struct {
			Type string `json:"type"`
		}
		json.Unmarshal([]byte(ev), &t)
		io.WriteString(w, "event: "+t.Type+"\ndata: "+ev+"\n\n")
	}
}

// What the last reply wrote to the vendor's cache counts with what it
// read: the first turn of a conversation reads nothing and writes the
// whole prompt, and its second turn stays to read it (vilicvane on
// Discord: 「会话保持 → 自动」 moved the second turn as "no-cache", and the
// cache just built was thrown away). Kept across a restart too, as the
// file has it; a reply that neither read nor wrote enough still moves.
func TestAffinityKeepsACacheJustWritten(t *testing.T) {
	const (
		first = `{"model":"cw/m","messages":[{"role":"user","content":"hi"}]}`
		again = `{"model":"cw/m","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"yo"},{"role":"user","content":"more"}]}`
	)
	for _, c := range []struct {
		name        string
		read, wrote int
		restart     bool
		why         string
	}{
		{"written", 0, 3000, false, "cache"},
		{"written, restarted", 0, 3000, true, "cache"},
		{"read and written together", 600, 600, false, "cache"},
		{"too little either way", 300, 300, false, "no-cache"},
	} {
		fresh(t)
		v := &cacheWriter{read: c.read, wrote: c.wrote}
		up := httptest.NewServer(v)
		t.Cleanup(up.Close)
		p := provider.Provider{ID: "cw", Name: "CW", Key: "k1", Models: []string{"m"}, Anthropic: up.URL + "/v1",
			Keys: []provider.KeyAccount{{Key: "k2"}}}
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
		s := New()
		if code, body := postAs(t, s, "s1", first); code != 200 {
			t.Fatalf("%s: first turn %d %s", c.name, code, body)
		}
		if c.restart {
			sticks.Lock()
			sticks.m, sticks.from, sticks.mod = map[string]stick{}, "", time.Time{}
			sticks.Unlock()
			s = New()
		}
		if code, body := postAs(t, s, "s1", again); code != 200 {
			t.Fatalf("%s: second turn %d %s", c.name, code, body)
		}
		a := s.trace.routes[len(s.trace.routes)-1].Affinity
		if a == nil || a.Why != c.why || a.Kept != (c.why == "cache") {
			t.Fatalf("%s: affinity %+v, want %s", c.name, a, c.why)
		}
		if a.CacheRead != c.read || a.CacheWrite != c.wrote {
			t.Fatalf("%s: trace tells read %d wrote %d, want %d and %d", c.name, a.CacheRead, a.CacheWrite, c.read, c.wrote)
		}
	}
}
