package core_test

import (
	"os/exec"
	"strings"
	"testing"
)

const mihomo = "github.com/metacubex/mihomo"

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go tool")
	}
	out, err := exec.Command("go", append([]string{"list"}, args...)...).Output()
	if err != nil {
		t.Fatalf("go list %v: %v", args, err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// Everything but the mihomo core speaks to the engine through core.Core.
func TestOnlyTheCoreImportsMihomo(t *testing.T) {
	for _, line := range goList(t, "-tags", "with_gvisor", "-f", `{{.ImportPath}} {{join .Imports " "}} {{join .TestImports " "}}`, "github.com/readeem/omihomo/...") {
		pkg, imports, _ := strings.Cut(line, " ")
		if pkg == "github.com/readeem/omihomo/internal/core/mihomo" {
			continue
		}
		for _, imp := range strings.Fields(imports) {
			if imp == mihomo || strings.HasPrefix(imp, mihomo+"/") {
				t.Errorf("%s imports %s", pkg, imp)
			}
		}
	}
}

// The controller, with its listener, secret and the self-update only it
// calls, is never linked.
func TestTheBinaryHasNoController(t *testing.T) {
	for _, dep := range goList(t, "-tags", "with_gvisor", "-deps", "github.com/readeem/omihomo/cmd/omihomo") {
		if dep == mihomo+"/hub" || strings.HasPrefix(dep, mihomo+"/hub/route") {
			t.Errorf("the binary links %s", dep)
		}
	}
}
