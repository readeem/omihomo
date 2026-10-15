package mihomo

import (
	"path/filepath"
	"slices"
	"strings"
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
