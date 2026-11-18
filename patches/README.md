# Local Go dependency patches

`go.mod` and `go.sum` pin the upstream modules. Each directory here names a
module in its `module` file and contains patches applied in filename order.
`tools/with-go-patches` applies them to private copies and passes a temporary
module file to Go. CI, development tasks, and release builds use the same
runner. The release vendor archive contains the patched source and its
upstream licenses.

For a focused test with the release dependencies, run
`mise run go -- test ./internal/core/mihomo -run TestHysteriaReceivesDatagramsInPeerOrder`.
Update the upstream pin through `go.mod` and `go.sum`. If a patch fails to
apply after the bump, inspect the upstream change and run its regression
before removing or refreshing the patch. Delete the module directory when
its last patch is removed.
