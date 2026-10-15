package profile

import (
	"fmt"
	"slices"
	"strings"

	"github.com/readeem/omihomo/internal/core"
)

// commaValues are the rule types whose value may hold commas, as mihomo
// reads them: the target is the last field, and there are no params.
var commaValues = []string{"NOT", "OR", "AND", "SUB-RULE", "DOMAIN-REGEX", "PROCESS-NAME-REGEX", "PROCESS-PATH-REGEX"}

// ParseRule splits a rule line such as "DOMAIN-SUFFIX,example.com,Proxy" or
// "AND,((DOMAIN,a.com),(NETWORK,UDP)),REJECT" the way mihomo does. Source is
// left empty.
func ParseRule(line string) (core.Rule, error) {
	fields := strings.Split(line, ",")
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	typ := strings.ToUpper(fields[0])
	switch {
	case typ == "MATCH" && len(fields) == 2 && fields[1] != "":
		return core.Rule{Type: typ, Target: fields[1]}, nil
	case slices.Contains(commaValues, typ) && len(fields) >= 3 && fields[len(fields)-1] != "":
		return core.Rule{Type: typ, Value: strings.Join(fields[1:len(fields)-1], ","), Target: fields[len(fields)-1]}, nil
	case typ != "MATCH" && !slices.Contains(commaValues, typ) && len(fields) >= 3 && fields[1] != "" && fields[2] != "":
		return core.Rule{Type: typ, Value: fields[1], Target: fields[2], Params: fields[3:]}, nil
	}
	return core.Rule{}, fmt.Errorf("rule %q is malformed", line)
}

// ruleTypes are the rule types a subscription may use. SUB-RULE is left
// out: sub-rules are not modelled.
var ruleTypes = []string{
	"DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-WILDCARD", "DOMAIN-REGEX", "GEOSITE",
	"GEOIP", "SRC-GEOIP", "IP-ASN", "SRC-IP-ASN", "IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR",
	"IP-SUFFIX", "SRC-IP-SUFFIX", "SRC-PORT", "DST-PORT", "IN-PORT", "IN-TYPE", "IN-USER", "IN-NAME",
	"PROCESS-PATH", "PROCESS-PATH-REGEX", "PROCESS-NAME", "PROCESS-NAME-REGEX", "UID", "NETWORK",
	"DSCP", "RULE-SET", "AND", "OR", "NOT", "MATCH",
}

// ruleParams are the trailing flags a rule may carry.
var ruleParams = []string{"no-resolve", "src"}
