package mihomo

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/metacubex/mihomo/component/geodata/router"
	"google.golang.org/protobuf/proto"

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
		if err := checkGeodata(f, path, core.Profile{}); err == nil {
			t.Errorf("%s took garbage", filepath.Base(f.Path))
		}
	}
}

// A site list must hold the categories the rules match.
func TestASiteListNeedsTheCategoriesInUse(t *testing.T) {
	list := &router.GeoSiteList{Entry: []*router.GeoSite{
		{CountryCode: "CN", Domain: []*router.Domain{{Type: router.Domain_Domain, Value: "example.cn"}}},
	}}
	data, err := proto.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "site")
	f := core.GeodataFile{Path: "/x/GeoSite.dat"}
	uses := func(rules ...core.Rule) core.Profile { return core.Profile{Rules: rules} }
	os.WriteFile(path, data, 0o600)
	if err := checkGeodata(f, path, uses(core.Rule{Type: "GEOSITE", Value: "cn", Target: "DIRECT"},
		core.Rule{Type: "NOT", Value: "((GEOSITE,!CN@ads))", Target: "DIRECT"})); err != nil {
		t.Fatalf("a list with the category = %v", err)
	}
	if err := checkGeodata(f, path, uses(core.Rule{Type: "GEOSITE", Value: "google", Target: "DIRECT"})); err == nil {
		t.Error("a list without the category was taken")
	}
	// mihomo loads CN whatever the rules use, and builds every category.
	for name, bad := range map[string]*router.GeoSiteList{
		"no CN": {Entry: []*router.GeoSite{{CountryCode: "GOOGLE", Domain: []*router.Domain{{Type: router.Domain_Domain, Value: "google.com"}}}}},
		"broken regex": {Entry: []*router.GeoSite{
			list.Entry[0],
			{CountryCode: "GOOGLE", Domain: []*router.Domain{{Type: router.Domain_Regex, Value: "["}}},
		}},
	} {
		data, _ := proto.Marshal(bad)
		os.WriteFile(path, data, 0o600)
		if err := checkGeodata(f, path, uses(core.Rule{Type: "GEOSITE", Value: "google", Target: "DIRECT"})); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	// One empty entry is a valid message but no list.
	os.WriteFile(path, []byte{0x0a, 0x00}, 0o600)
	if err := checkGeodata(f, path, core.Profile{}); err == nil {
		t.Error("an empty site list was taken")
	}
}
