package daemon

import (
	"context"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/readeem/omihomo/internal/protocol"
	"github.com/readeem/omihomo/internal/state"
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

func (d *server) addRule(ctx context.Context, args protocol.RuleArgs) (any, *protocol.Error) {
	typ := strings.ToUpper(strings.TrimSpace(args.Type))
	if !slices.Contains(userRuleTypes, typ) {
		return nil, fail(protocol.CodeInvalid, "rule type must be one of %s", strings.Join(userRuleTypes, ", "))
	}
	if err := checkRuleValue(typ, args.Value); err != nil {
		return nil, err
	}
	if err := validName("target", args.Target); err != nil {
		return nil, err
	}
	if strings.Contains(args.Target, ",") {
		return nil, fail(protocol.CodeInvalid, "a rule target cannot contain a comma")
	}

	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	next, p := d.settings, d.profile
	d.mu.Unlock()
	if !hasTarget(p, args.Target) {
		return nil, fail(protocol.CodeNotFound, "the active subscription has no group or proxy %q", args.Target)
	}
	if len(next.Rules) >= maxUserRules {
		return nil, fail(protocol.CodeInvalid, "at most %d rules", maxUserRules)
	}
	next.Rules = append(slices.Clone(next.Rules), state.Rule{Type: typ, Value: args.Value, Target: args.Target})
	return d.commitRules(ctx, next)
}

func (d *server) removeRule(ctx context.Context, index int) (any, *protocol.Error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	next := d.settings
	d.mu.Unlock()
	if index < 1 || index > len(next.Rules) {
		return nil, fail(protocol.CodeNotFound, "there is no rule %d of yours", index)
	}
	next.Rules = slices.Delete(slices.Clone(next.Rules), index-1, index)
	return d.commitRules(ctx, next)
}

// commitRules runs and stores next. The caller holds opMu.
func (d *server) commitRules(ctx context.Context, next state.Settings) (any, *protocol.Error) {
	d.mu.Lock()
	p := d.profile
	d.mu.Unlock()
	if err := d.reapply(ctx, next, p); err != nil {
		return nil, err
	}
	if err := d.saveSettings(next); err != nil {
		return nil, err
	}
	d.publish(ctx)
	return d.rules(ctx)
}
