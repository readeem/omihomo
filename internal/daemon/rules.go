package daemon

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/readeem/omihomo/internal/protocol"
)

// userRuleTypes are the rule types the owner can add. Each value is checked
// for its type, so no rule can carry another rule's fields.
var userRuleTypes = []string{
	"DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-WILDCARD", "GEOSITE",
	"IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR", "GEOIP",
	"DST-PORT", "SRC-PORT", "PROCESS-NAME", "PROCESS-PATH", "NETWORK",
}

// maxUserRules bounds the owner's rules.
const maxUserRules = 1000

func checkRuleValue(typ, value string) *protocol.Error {
	if value == "" || len(value) > maxName || value != strings.TrimSpace(value) ||
		strings.ContainsFunc(value, unicode.IsControl) {
		return fail(protocol.CodeInvalid, "the value is empty, too long, or has surrounding space or control characters")
	}
	if strings.Contains(value, ",") {
		return fail(protocol.CodeInvalid, "a rule value cannot contain a comma")
	}
	switch typ {
	case "IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR":
		p, err := netip.ParsePrefix(value)
		if err != nil || typ == "IP-CIDR6" && !p.Addr().Is6() {
			return fail(protocol.CodeInvalid, "invalid %s %q", typ, value)
		}
	case "DST-PORT", "SRC-PORT":
		lo, hi, ok := strings.Cut(value, "-")
		if !ok {
			hi = lo
		}
		a, err1 := strconv.ParseUint(lo, 10, 16)
		b, err2 := strconv.ParseUint(hi, 10, 16)
		if err1 != nil || err2 != nil || a > b {
			return fail(protocol.CodeInvalid, "invalid %s %q: a port or a range like 8000-8080", typ, value)
		}
	case "NETWORK":
		if v := strings.ToLower(value); v != "tcp" && v != "udp" {
			return fail(protocol.CodeInvalid, "NETWORK is tcp or udp")
		}
	case "PROCESS-NAME", "PROCESS-PATH":
	default:
		if strings.ContainsAny(value, " /\\") {
			return fail(protocol.CodeInvalid, "invalid %s %q", typ, value)
		}
	}
	return nil
}

func checkRules(rules []protocol.UserRule) *protocol.Error {
	if len(rules) > maxUserRules {
		return fail(protocol.CodeInvalid, "at most %d rules", maxUserRules)
	}
	for i, r := range rules {
		if err := checkRule(r); err != nil {
			err.Message = fmt.Sprintf("rule %d: %s", i+1, err.Message)
			return err
		}
	}
	return nil
}

func checkRule(r protocol.UserRule) *protocol.Error {
	if !slices.Contains(userRuleTypes, r.Type) {
		return fail(protocol.CodeInvalid, "the type must be one of %s", strings.Join(userRuleTypes, ", "))
	}
	if err := checkRuleValue(r.Type, r.Value); err != nil {
		return err
	}
	if err := validName("target", r.Target); err != nil {
		return err
	}
	if strings.Contains(r.Target, ",") {
		return fail(protocol.CodeInvalid, "a rule target cannot contain a comma")
	}
	return nil
}
