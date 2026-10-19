package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/readeem/omihomo/internal/cli"
)

func TestMigrationPreflightLeavesLegacyDataUntouched(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "old")
	if err := os.MkdirAll(filepath.Join(data, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"subscriptions.json": `[{"name":"Example","url":"https://example.com/sub"}]`,
		"active":             "Example",
		"cache/Example.yaml": "proxies: [{name: Example, type: direct}]\nproxy-groups: [{name: Proxy, type: select, proxies: [Example]}]\nrules: [MATCH,Proxy]\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(data, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", dir)
	t.Setenv("OMIHOMO_SOCKET", filepath.Join(dir, "no-daemon"))
	code, out, errOut := run(t, "", "preflight", "--legacy-data", data)
	var result struct {
		Import bool `json:"import"`
	}
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &result) != nil || !result.Import {
		t.Fatalf("preflight: exit=%d out=%s err=%s", code, out, errOut)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(data, name))
		if err != nil || string(got) != want {
			t.Fatalf("legacy %s changed: %q, %v", name, got, err)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 || entries[0].Name() != "old" {
		t.Fatalf("staging left behind: %v, %v", entries, err)
	}
	if err := os.WriteFile(filepath.Join(data, "override.yaml"), []byte("rules:\n  append: [MATCH,DIRECT]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = run(t, "", "preflight", "--legacy-data", data)
	if code != cli.ExitFailure || !strings.Contains(errOut, "convert them to typed prepend rules") {
		t.Fatalf("unsafe rules passed preflight: exit=%d err=%s", code, errOut)
	}
}
