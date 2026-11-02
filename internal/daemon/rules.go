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
var userRuleTypes = []string{"SUB-RULE",
	"DOMAIN-REGEX", "PROCESS-NAME-REGEX", "PROCESS-PATH-REGEX", "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-WILDCARD", "GEOSITE",
	"IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR", "GEOIP", "SRC-GEOIP", "IP-ASN", "SRC-IP-ASN",
	"IP-SUFFIX", "SRC-IP-SUFFIX", "DST-PORT", "SRC-PORT", "IN-PORT", "IN-TYPE", "IN-USER", "IN-NAME",
	"REMATCH-NAME", "PROCESS-NAME", "PROCESS-PATH", "PROCESS-NAME-WILDCARD", "PROCESS-PATH-WILDCARD",
	"UID", "NETWORK", "DSCP", "RULE-SET", "AND", "OR", "NOT", "MATCH",
}

// maxUserRules bounds the owner's rules.
const maxUserRules = 1000

func checkRuleValue(typ, value string) *protocol.Error {
	if typ == "MATCH" {
		if value != "" {
			return fail(protocol.CodeInvalid, "MATCH takes no value")
		}
		return nil
	}
	if value == "" || len(value) > 4096 || value != strings.TrimSpace(value) ||
		strings.ContainsFunc(value, unicode.IsControl) {
		return fail(protocol.CodeInvalid, "the value is empty, too long, or has surrounding space or control characters")
	}
	if strings.Contains(value, ",") && typ != "AND" && typ != "OR" && typ != "NOT" && typ != "SUB-RULE" && !strings.HasSuffix(typ, "-REGEX") {
		return fail(protocol.CodeInvalid, "a rule value cannot contain a comma")
	}
	switch typ {
	case "IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR":
		p, err := netip.ParsePrefix(value)
		if err != nil || typ == "IP-CIDR6" && !p.Addr().Is6() {
			return fail(protocol.CodeInvalid, "invalid %s %q", typ, value)
		}
	case "DST-PORT", "SRC-PORT", "IN-PORT", "UID", "DSCP":
		limit := uint64(65535)
		if typ == "UID" {
			limit = 4294967295
		}
		if typ == "DSCP" {
			limit = 63
		}
		parts := strings.Split(value, "/")
		if len(parts) > 28 {
			return fail(protocol.CodeInvalid, "at most 28 ranges")
		}
		for _, part := range parts {
			lo, hi, ok := strings.Cut(part, "-")
			if !ok {
				hi = lo
			}
			a, err1 := strconv.ParseUint(lo, 10, 32)
			b, err2 := strconv.ParseUint(hi, 10, 32)
			if err1 != nil || err2 != nil || a > b || b > limit {
				return fail(protocol.CodeInvalid, "invalid %s range %q", typ, part)
			}
		}
	case "NETWORK":
		if v := strings.ToLower(value); v != "tcp" && v != "udp" {
			return fail(protocol.CodeInvalid, "NETWORK is tcp or udp")
		}
	case "IP-ASN", "SRC-IP-ASN":
		_, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return fail(protocol.CodeInvalid, "invalid %s", typ)
		}
	case "IP-SUFFIX", "SRC-IP-SUFFIX":
		if _, err := netip.ParsePrefix(value); err != nil {
			return fail(protocol.CodeInvalid, "invalid IP suffix")
		}
	case "DOMAIN-REGEX", "PROCESS-NAME-REGEX", "PROCESS-PATH-REGEX":
	case "PROCESS-NAME", "PROCESS-PATH", "PROCESS-NAME-WILDCARD", "PROCESS-PATH-WILDCARD", "IN-TYPE", "IN-USER", "IN-NAME", "REMATCH-NAME":
	case "SUB-RULE":
		if !strings.HasPrefix(value, "(") || !strings.HasSuffix(value, ")") {
			return fail(protocol.CodeInvalid, "use a parenthesized condition")
		}
	case "AND", "OR", "NOT":
		if !strings.HasPrefix(value, "((") || !strings.HasSuffix(value, "))") {
			return fail(protocol.CodeInvalid, "use a parenthesized logic expression")
		}
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
	for param := range strings.FieldsSeq(r.Params) {
		if param != "no-resolve" && param != "src" {
			return fail(protocol.CodeInvalid, "unknown rule parameter %q", param)
		}
		if !slices.Contains([]string{"GEOIP", "IP-ASN", "IP-CIDR", "IP-CIDR6", "IP-SUFFIX", "RULE-SET"}, r.Type) {
			return fail(protocol.CodeInvalid, "%s takes no parameters", r.Type)
		}
	}
	if err := validName("target", r.Target); err != nil {
		return err
	}
	if strings.Contains(r.Target, ",") {
		return fail(protocol.CodeInvalid, "a rule target cannot contain a comma")
	}
	return nil
}
