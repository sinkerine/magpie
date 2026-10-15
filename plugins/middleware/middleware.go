// Package middleware is the JS gateway middleware: the plugins with a
// middleware file in plugins.json, run on agents' requests and replies in
// the gateway's own process (docs/subsystems/gateway-middleware.md). While
// one is on in plugins.json, it stays on.
//
// It is the agent middleware of the gateway's middleware point (P3 of
// docs/subsystems/plugin-runtime.md): Apply registers internal/middleware's
// chain, in the same place, with the same API and the same fail-open
// policy as before; off, agents' requests go through no middleware.
package middleware

import (
	"github.com/yetone/magpie/internal/gwmw"
	"github.com/yetone/magpie/internal/middleware"
	"github.com/yetone/magpie/internal/pluginrt"
)

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "middleware", API: "^1", Requires: []string{"gateway"}, Essential: true,
		Name: "Gateway middleware", Sub: "Run the JavaScript middleware plugins on agents' requests and replies",
		One: "1 middleware on", Many: "{n} middleware on",
		InUse: func() (int, error) { return middleware.On(), nil },
		Apply: func(c *pluginrt.Ctx) error {
			c.OnDispose(gwmw.Use(middleware.Chain{}))
			return nil
		},
	})
}
