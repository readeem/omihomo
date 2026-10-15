package mihomo

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/metacubex/mihomo/common/convert"
	"github.com/metacubex/mihomo/component/geodata/router"
	"github.com/metacubex/mihomo/component/mmdb"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	mlog "github.com/metacubex/mihomo/log"

	"google.golang.org/protobuf/proto"

	"github.com/readeem/omihomo/internal/core"
)

// ParseLinks silences mihomo's log while it converts: the converter logs a
// link it cannot read whole, credentials included. It holds the apply lock,
// which is what else sets the log level.
func (c *Core) ParseLinks(data []byte) ([]map[string]any, error) {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	level := mlog.Level()
	mlog.SetLevel(mlog.SILENT)
	defer mlog.SetLevel(level)
	return convert.ConvertsV2Ray(data)
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
	for _, r := range p.Rules {
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
		out = append(out, core.GeodataFile{Path: C.Path.MMDB(), URL: urls.Mmdb})
	}
	if need["GEOSITE"] {
		out = append(out, core.GeodataFile{Path: C.Path.GeoSite(), URL: urls.GeoSite})
	}
	if need["IP-ASN"] || need["SRC-IP-ASN"] {
		out = append(out, core.GeodataFile{Path: C.Path.ASN(), URL: urls.ASN})
	}
	return out
}

func (c *Core) CheckGeodata(f core.GeodataFile, path string) error {
	switch filepath.Base(f.Path) {
	case filepath.Base(C.Path.GeoSite()):
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var list router.GeoSiteList
		if err := proto.Unmarshal(data, &list); err != nil || len(list.Entry) == 0 {
			return errors.New("not a site list")
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
