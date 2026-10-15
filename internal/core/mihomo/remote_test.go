package mihomo

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/readeem/omihomo/internal/core"
)

func TestParseLinks(t *testing.T) {
	got, err := shared.ParseLinks([]byte("trojan://pw@example.com:443?sni=example.com#Berlin\n"))
	if err != nil || len(got) != 1 || got[0]["name"] != "Berlin" || got[0]["type"] != "trojan" {
		t.Fatalf("links = %v, %v", got, err)
	}
	if _, err := shared.ParseLinks([]byte("not a link")); err == nil {
		t.Error("garbage parsed as links")
	}
}

func TestGeodataFollowsTheRules(t *testing.T) {
	files := func(p core.Profile) []string {
		var out []string
		for _, f := range shared.Geodata(p) {
			if !strings.HasPrefix(f.URL, "https://") || !filepath.IsAbs(f.Path) {
				t.Errorf("geodata %+v", f)
			}
			out = append(out, filepath.Base(f.Path))
		}
		return out
	}
	if got := files(profile()); len(got) != 0 {
		t.Errorf("a profile without geo rules needs %v", got)
	}
	p := core.Profile{
		Rules: []core.Rule{
			{Type: "AND", Value: "((geoip,CN),(NETWORK,UDP))", Target: "DIRECT"},
			{Type: "DOMAIN", Value: "geosite.example", Target: "DIRECT"},
		},
		RuleSets: []core.RuleSet{{Name: "c", Behavior: "classical", Payload: []string{"IP-ASN,13335"}}},
	}
	if got := files(p); !slices.Equal(got, []string{"geoip.metadb", "GeoLite2-ASN.mmdb"}) && !slices.Equal(got, []string{"geoip.metadb", "ASN.mmdb"}) {
		t.Errorf("geodata %v", got)
	}
}

// A link the converter cannot read is logged by it, token and all; none of
// that may reach the journal.
func TestParseLinksLogsNoCredentials(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// mihomo writes to the stdout it found at init, so the descriptor is
	// what has to move.
	saved, err := syscall.Dup(1)
	if err != nil {
		t.Fatal(err)
	}
	syscall.Dup2(int(w.Fd()), 1)
	shared.ParseLinks([]byte("vmess://not-base64-s3cret\nvless://s3cret@[bad::host:443?x=%zz#Bad\ntrojan://pw@example.com:443#Ok\n"))
	syscall.Dup2(saved, 1)
	syscall.Close(saved)
	w.Close()
	out, _ := io.ReadAll(r)
	if strings.Contains(string(out), "s3cret") {
		t.Fatalf("logged %q", out)
	}
}

func TestBrokenGeodataIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "download")
	os.WriteFile(path, []byte("<html>not a database</html>"), 0o600)
	for _, f := range []core.GeodataFile{{Path: "/x/geoip.metadb"}, {Path: "/x/GeoLite2-ASN.mmdb"}, {Path: "/x/GeoSite.dat"}} {
		if err := shared.CheckGeodata(f, path); err == nil {
			t.Errorf("%s took garbage", filepath.Base(f.Path))
		}
	}
}
