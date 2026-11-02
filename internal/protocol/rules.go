package protocol

import "strings"

var RuleCategories = []string{"domains", "addresses", "applications", "ports", "inbound", "rule-sets", "logic", "other"}

func RuleCategory(typ string) string {
	switch {
	case strings.HasPrefix(typ, "DOMAIN"), typ == "GEOSITE":
		return "domains"
	case strings.Contains(typ, "IP"), typ == "GEOIP", typ == "SRC-GEOIP":
		return "addresses"
	case strings.HasPrefix(typ, "PROCESS"), typ == "UID":
		return "applications"
	case strings.HasSuffix(typ, "-PORT"):
		return "ports"
	case strings.HasPrefix(typ, "IN-"), typ == "REMATCH-NAME":
		return "inbound"
	case typ == "RULE-SET":
		return "rule-sets"
	case typ == "AND", typ == "OR", typ == "NOT", typ == "SUB-RULE":
		return "logic"
	default:
		return "other"
	}
}

// CategoryTargets applies after subscription refreshes without changing rule order.
type CategoryTargets struct {
	Domains      string `json:"domains,omitempty"`
	Addresses    string `json:"addresses,omitempty"`
	Applications string `json:"applications,omitempty"`
	Ports        string `json:"ports,omitempty"`
	Inbound      string `json:"inbound,omitempty"`
	RuleSets     string `json:"rule-sets,omitempty"`
	Logic        string `json:"logic,omitempty"`
	Other        string `json:"other,omitempty"`
}

func (t *CategoryTargets) Field(category string) *string {
	switch category {
	case "domains":
		return &t.Domains
	case "addresses":
		return &t.Addresses
	case "applications":
		return &t.Applications
	case "ports":
		return &t.Ports
	case "inbound":
		return &t.Inbound
	case "rule-sets":
		return &t.RuleSets
	case "logic":
		return &t.Logic
	default:
		return &t.Other
	}
}
