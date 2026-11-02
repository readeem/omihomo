package mihomo

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/metacubex/mihomo/common/convert"
	"github.com/metacubex/mihomo/component/geodata"
	"github.com/metacubex/mihomo/component/geodata/router"
	"github.com/metacubex/mihomo/component/mmdb"
	"github.com/metacubex/mihomo/config"
	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"

	"google.golang.org/protobuf/proto"

	"github.com/readeem/omihomo/internal/core"
)

// ParseLinks silences mihomo's log while it converts: the converter logs a
// link it cannot read whole, credentials included. It holds the apply lock,
// which is what else sets the log level.
func (c *Core) ParseLinks(data []byte) ([]map[string]any, error) {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	level := log.Level()
	log.SetLevel(log.SILENT)
	defer log.SetLevel(level)
	return convert.ConvertsV2Ray(data)
}

func geodataRules(p core.Profile) []core.Rule {
	rules := slices.Clone(p.Rules)
	for _, sub := range p.SubRules {
		rules = append(rules, sub...)
	}
	return rules
}

// geoRule finds the rule types that load geodata, on their own or nested in
// a logic rule: GEOIP and SRC-GEOIP read the MMDB, GEOSITE the site list,
// and IP-ASN and SRC-IP-ASN the ASN database.
var geoRule = regexp.MustCompile(`(?i)(?:^|[(,])\s*(SRC-GEOIP|GEOIP|GEOSITE|SRC-IP-ASN|IP-ASN)\s*,`)

func (c *Core) Geodata(p core.Profile) []core.GeodataFile {
	need := map[string]bool{}
	scan := func(line string) {
		for _, m := range geoRule.FindAllStringSubmatch(line, -1) {
			need[strings.ToUpper(m[1])] = true
		}
	}
	for _, r := range geodataRules(p) {
		scan(r.Type + "," + r.Value)
	}
	for _, rs := range p.RuleSets {
		if rs.Behavior == "classical" {
			for _, line := range rs.Payload {
				scan(line)
			}
		}
	}
	urls := config.DefaultRawConfig().GeoXUrl
	var out []core.GeodataFile
	if need["GEOIP"] || need["SRC-GEOIP"] {
		out = append(out, core.GeodataFile{Path: constant.Path.MMDB(), URL: urls.Mmdb})
	}
	if need["GEOSITE"] {
		out = append(out, core.GeodataFile{Path: constant.Path.GeoSite(), URL: urls.GeoSite})
	}
	if need["IP-ASN"] || need["SRC-IP-ASN"] {
		out = append(out, core.GeodataFile{Path: constant.Path.ASN(), URL: urls.ASN})
	}
	return out
}

// siteRule finds the categories GEOSITE rules match, on their own or nested
// in a logic rule.
var siteRule = regexp.MustCompile(`(?i)(?:^|[(,])\s*GEOSITE\s*,\s*([^,()]+)`)

// siteCategories are the GEOSITE categories p's rules match, without
// negation or attributes.
func siteCategories(p core.Profile) []string {
	var out []string
	scan := func(line string) {
		for _, m := range siteRule.FindAllStringSubmatch(line, -1) {
			name, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(m[1]), "!"), "@")
			if name != "" && !slices.ContainsFunc(out, func(s string) bool { return strings.EqualFold(s, name) }) {
				out = append(out, name)
			}
		}
	}
	for _, r := range geodataRules(p) {
		scan(r.Type + "," + r.Value)
	}
	for _, rs := range p.RuleSets {
		if rs.Behavior == "classical" {
			for _, line := range rs.Payload {
				scan(line)
			}
		}
	}
	return out
}

func (c *Core) InstallGeodata(f core.GeodataFile, path string, p core.Profile) error {
	if err := checkGeodata(f, path, p); err != nil {
		return err
	}
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	if err := os.Rename(path, f.Path); err != nil {
		return err
	}
	switch filepath.Base(f.Path) {
	case filepath.Base(constant.Path.GeoSite()):
		geodata.ClearGeoSiteCache()
	case filepath.Base(constant.Path.MMDB()):
		c.reloadIP = true
	case filepath.Base(constant.Path.ASN()):
		c.reloadASN = true
	}
	return nil
}

func (c *Core) reloadDatabases() {
	if c.reloadIP {
		mmdb.ReloadIP()
		c.reloadIP = false
	}
	if c.reloadASN {
		mmdb.ReloadASN()
		c.reloadASN = false
	}
}

// legacyGeodata lists each name the daemon finds a geodata file under, with
// the names an old core may have cached it as, in the order mihomo picks
// them.
var legacyGeodata = []struct {
	name string
	from []string
}{
	{"geoip.metadb", []string{"Country.mmdb", "geoip.metadb"}},
	{"GeoSite.dat", []string{"GeoSite.dat"}},
	{"ASN.mmdb", []string{"ASN.mmdb"}},
}

// maxGeodata matches the daemon's download limit.
const maxGeodata = 64 << 20

// ImportGeodata copies into an unused core home each geodata file an old
// core cached in legacy that is usable for the rules of p, keeping its age.
// The caller must own home exclusively. A file that is missing, a link, too
// large or unusable is skipped and left for the daemon to download.
func (*Core) ImportGeodata(home string, legacy *os.Root, p core.Profile) error {
	for _, g := range legacyGeodata {
		for _, from := range g.from {
			staged, err := importGeodata(legacy, from, filepath.Join(home, g.name), p)
			if err != nil {
				return err
			}
			if staged {
				break
			}
		}
	}
	return nil
}

// importGeodata returns an error only for a failure to write into home.
func importGeodata(legacy *os.Root, from, dst string, p core.Profile) (bool, error) {
	src, err := legacy.OpenFile(from, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, nil
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxGeodata {
		return false, nil
	}
	data, err := io.ReadAll(io.LimitReader(src, maxGeodata+1))
	if err != nil || len(data) > maxGeodata {
		return false, nil
	}
	if err := privateDir(filepath.Dir(dst)); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+"-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chtimes(tmp.Name(), info.ModTime(), info.ModTime())
	}
	if err != nil {
		return false, err
	}
	if checkGeodata(core.GeodataFile{Path: dst}, tmp.Name(), p) != nil {
		return false, nil
	}
	return true, os.Rename(tmp.Name(), dst)
}

// checkGeodata tells the files apart by name alone: mihomo's own paths are
// empty in a process that does not host it.
func checkGeodata(f core.GeodataFile, path string, p core.Profile) error {
	switch {
	case strings.EqualFold(filepath.Base(f.Path), "GeoSite.dat"):
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var list router.GeoSiteList
		if err := proto.Unmarshal(data, &list); err != nil {
			return errors.New("not a site list")
		}
		domains := 0
		for _, e := range list.Entry {
			domains += len(e.Domain)
		}
		if domains == 0 {
			return errors.New("the site list is empty")
		}
		// mihomo loads CN whenever it opens the list, and removes a list
		// where that fails; the rules need their own categories too. Each
		// is built the way mihomo builds it.
		for _, name := range append([]string{"CN"}, siteCategories(p)...) {
			i := slices.IndexFunc(list.Entry, func(e *router.GeoSite) bool {
				return strings.EqualFold(e.CountryCode, name) && len(e.Domain) > 0
			})
			if i < 0 {
				return fmt.Errorf("the site list has no category %q", name)
			}
			if _, err := router.NewSuccinctMatcherGroup(list.Entry[i].Domain); err != nil {
				return fmt.Errorf("the site list's category %q does not load", name)
			}
		}
		return nil
	default:
		// The MMDB and the ASN database are both MaxMind databases, and
		// mihomo exits on one that does not open.
		if !mmdb.Verify(path) {
			return errors.New("not a MaxMind database")
		}
		return nil
	}
}
