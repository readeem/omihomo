package core_test

import (
	"os/exec"
	"strings"
	"testing"
)

const mixray = "github.com/readeem/mixray"

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

func TestOnlyTheAdapterImportsProxyEngines(t *testing.T) {
	engines := []string{
		mixray,
		"github.com/metacubex/mihomo",
		"github.com/xtls/xray-core",
	}
	for _, line := range goList(t, "-tags", "with_gvisor", "-f", `{{.ImportPath}} {{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}`, "github.com/readeem/omihomo/...") {
		pkg, imports, _ := strings.Cut(line, " ")
		for imp := range strings.FieldsSeq(imports) {
			for _, root := range engines {
				if root == mixray && pkg == "github.com/readeem/omihomo/internal/core/mihomo" {
					continue
				}
				if imp == root || strings.HasPrefix(imp, root+"/") {
					t.Errorf("%s imports %s", pkg, imp)
				}
			}
		}
	}
}

func TestTheBinaryOmitsMixrayControllerAndXrayCLI(t *testing.T) {
	for _, dep := range goList(t, "-tags", "with_gvisor", "-deps", "github.com/readeem/omihomo/cmd/omihomo") {
		if dep == mixray+"/hub" || strings.HasPrefix(dep, mixray+"/hub/route") ||
			dep == mixray+"/internal/xray-core/main" || strings.HasPrefix(dep, mixray+"/internal/xray-core/main/distro/") {
			t.Errorf("the binary links %s", dep)
		}
	}
}
