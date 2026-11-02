package hosting_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestOnlyHostingAdapterImportsXray(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}} {{join .TestImports " "}}`, "github.com/readeem/omihomo/...")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		pkg, imports, _ := strings.Cut(line, " ")
		if pkg == "github.com/readeem/omihomo/internal/hosting/xray" {
			continue
		}
		for imported := range strings.FieldsSeq(imports) {
			if strings.HasPrefix(imported, "github.com/xtls/xray-core/") || imported == "github.com/xtls/xray-core" {
				t.Errorf("%s imports engine dependency %s outside its adapter", pkg, imported)
			}
		}
	}
}
