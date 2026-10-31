# Nix development and tests

`nix develop` (or direnv with `.envrc`) supplies Go, Bun, Bash, coreutils
and the C toolchain. Bun enables the local plugin-host fixtures; no plugin
package needs to be downloaded for those tests.

Run the Go suite with a temporary HOME and pinned caches as described in
[the code standards](code-standards.md#acceptance-criteria):

```sh
(
    verification_gopath=$(go env GOPATH) || exit
    verification_modcache=$(go env GOMODCACHE) || exit
    verification_buildcache=$(go env GOCACHE) || exit
    verification_home=$(mktemp -d) || exit
    trap 'chmod -R u+w "$verification_home"; rm -rf "$verification_home"' EXIT
    HOME="$verification_home" GOPATH="$verification_gopath" \
        GOMODCACHE="$verification_modcache" GOCACHE="$verification_buildcache" \
        go test -tags nogui ./...
)
```

NixOS provides `/bin/sh`, but programs such as `cat`, `mkdir`, `sleep` and
`env` live on PATH under the Nix store. Shell fixtures that narrow PATH
use `testenv.Tools(t, script)` with `{{cat}}`, `{{mkdir}}` or `{{sleep}}`
placeholders, resolved from the test binary's initial PATH to quoted
absolute paths. `testenv.ToolDir` supplies only the commands a simulated
WSL environment needs, keeping installed agents out of its PATH.

Sandboxed tests disable command-discovery caching, including directory
listings. A fixture can install a command within a filesystem timestamp's
resolution; its next lookup must see it even when the directory's recorded
modification time is unchanged. The application keeps its normal cache.

The `nogui` tag tests the web handlers and assets without linking a desktop
webview. Native Linux desktop builds additionally need the GTK/WebKit
libraries; browser tests require the Playwright setup described in
[the GUI test README](../internal/gui/tests/README.md).
