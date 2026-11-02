package protocol

// CategoryTargets is decoded only to reject legacy subscription overrides explicitly.
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

// PruneCategories preserves the order and contents of all nonempty entries.
func PruneCategories(entries []RuleEntry) []RuleEntry {
	out := make([]RuleEntry, 0, len(entries))
	for _, e := range entries {
		if e.Category == nil || len(e.Category.Rules) > 0 {
			out = append(out, e)
		}
	}
	return out
}
