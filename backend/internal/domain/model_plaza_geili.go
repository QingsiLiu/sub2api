package domain

// GroupModelPlazaConfig controls which models a group contributes to the
// user-facing model plaza. It deliberately remains separate from
// GroupModelAllowlist: this is presentation-only and never changes gateway
// admission or the /models response.
type GroupModelPlazaConfig struct {
	Mode   string   `json:"mode"`
	Models []string `json:"models,omitempty"`
}

const (
	GroupModelPlazaModeAll      = "all"
	GroupModelPlazaModeSelected = "selected"
)
