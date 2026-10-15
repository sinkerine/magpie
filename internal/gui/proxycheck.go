package gui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/netproxy"
)

// Settings › Proxy's Test (#2076, c4ex): whether the proxy in force is the
// way magpie's requests leave. One request to Cloudflare's trace page goes
// as a vendor request would (the proxy Settings, *_PROXY or the system
// names), and one goes direct; each says the address Cloudflare saw it
// from and the country. Different addresses say the proxy is taking the
// requests; the same address says they leave as if there were none.

// proxyTraceURL is the page asked; a test points it at its own server.
var proxyTraceURL = "https://www.cloudflare.com/cdn-cgi/trace"

// proxyTraceTimeout is how long each request may take.
const proxyTraceTimeout = 10 * time.Second

// proxyLeg is one of the two requests: the address and country Cloudflare
// saw it from, how long it took, or why it failed.
type proxyLeg struct {
	IP    string `json:"ip,omitempty"`
	Loc   string `json:"loc,omitempty"`
	Ms    int64  `json:"ms,omitempty"`
	Error string `json:"error,omitempty"`
}

// proxyCheck is the answer: the proxy in force (redacted) and where it came
// from, as Settings says it (netproxy.Describe), the request through it and
// the one without. Through is absent when no proxy is in force, since the
// request "through" it is the direct one.
type proxyCheck struct {
	Proxy   string    `json:"proxy,omitempty"`
	Source  string    `json:"source"`
	Through *proxyLeg `json:"through,omitempty"`
	Direct  proxyLeg  `json:"direct"`
}

// checkProxy asks the trace page through via (a context as a vendor
// request carries) and direct, at once. through is false when no proxy is
// in force: only the direct request is made.
func checkProxy(ctx context.Context, client *http.Client, via context.Context, through bool) proxyCheck {
	var out proxyCheck
	var wg sync.WaitGroup
	if through {
		out.Through = &proxyLeg{}
		wg.Add(1)
		go func() { defer wg.Done(); *out.Through = traceLeg(via, client) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); out.Direct = traceLeg(netproxy.With(ctx, "direct"), client) }()
	wg.Wait()
	return out
}

// traceLeg makes one request to the trace page and reads its ip= and loc=
// lines.
func traceLeg(ctx context.Context, client *http.Client) proxyLeg {
	ctx, cancel := context.WithTimeout(ctx, proxyTraceTimeout)
	defer cancel()
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyTraceURL, nil)
	if err != nil {
		return proxyLeg{Error: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		// a proxy's password can be in the error's URL
		return proxyLeg{Error: redactProxyErr(err.Error())}
	}
	defer resp.Body.Close()
	var leg proxyLeg
	leg.Ms = time.Since(start).Milliseconds()
	if resp.StatusCode != http.StatusOK {
		leg.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return leg
	}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 16<<10))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "ip":
			leg.IP = strings.TrimSpace(v)
		case "loc":
			leg.Loc = strings.TrimSpace(v)
		}
	}
	if leg.IP == "" {
		leg.Error = "no address in the answer"
	}
	return leg
}

// redactProxyErr takes a user:password@ out of an error's text.
func redactProxyErr(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if strings.Contains(w, "://") && strings.Contains(w, "@") {
			q := strings.Trim(w, `"':,`)
			if r := netproxy.Redact(q); r != "" {
				words[i] = strings.Replace(w, q, r, 1)
			} else {
				words[i] = "[proxy]"
			}
		}
	}
	return strings.Join(words, " ")
}

func handleProxyCheck(rw http.ResponseWriter, r *http.Request) {
	proxy, source := netproxy.Describe()
	through := source == "settings" || source == "environment" || source == "system"
	out := checkProxy(r.Context(), http.DefaultClient, r.Context(), through)
	out.Proxy, out.Source = proxy, source
	writeJSON(rw, out)
}
