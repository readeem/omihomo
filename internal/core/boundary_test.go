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

func TestOnlyTheAdaptersImportProxyEngines(t *testing.T) {
	engines := map[string]string{
		mihomo:                      "mihomo",
		"github.com/xtls/xray-core": "xray",
	}
	for _, line := range goList(t, "-tags", "with_gvisor", "-f", `{{.ImportPath}} {{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}`, "github.com/readeem/omihomo/...") {
		pkg, imports, _ := strings.Cut(line, " ")
		for imp := range strings.FieldsSeq(imports) {
			for root, adapter := range engines {
				if pkg != "github.com/readeem/omihomo/internal/core/"+adapter && (imp == root || strings.HasPrefix(imp, root+"/")) {
					t.Errorf("%s imports %s", pkg, imp)
				}
			}
		}
	}
}

func TestTheBinaryOmitsMihomoControllerAndXrayCLI(t *testing.T) {
	for _, dep := range goList(t, "-tags", "with_gvisor", "-deps", "github.com/readeem/omihomo/cmd/omihomo") {
		if dep == mihomo+"/hub" || strings.HasPrefix(dep, mihomo+"/hub/route") ||
			dep == "github.com/xtls/xray-core/main" || strings.HasPrefix(dep, "github.com/xtls/xray-core/main/distro/") {
			t.Errorf("the binary links %s", dep)
		}
	}
}
