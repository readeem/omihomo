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
