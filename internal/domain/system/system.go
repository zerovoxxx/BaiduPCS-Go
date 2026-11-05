package system

type Operation struct {
	ID                     string
	Method                 string
	Path                   string
	Phase                  string
	Scopes                 []string
	Anonymous              bool
	Implemented            bool
	Implementation         string
	SupportedOptions       []string
	CredentialAlternatives [][]string
	RequiredCredentials    []string
	SupportedValues        map[string][]string
	Limits                 []Limit
}
type Info struct {
	APIVersion     string `json:"api_version"`
	ProgramVersion string `json:"program_version"`
	SourceBaseline string `json:"source_baseline"`
	StartedAt      string `json:"started_at"`
	Now            string `json:"now"`
}
type Limit struct {
	Name       string  `json:"name"`
	Value      *string `json:"value"`
	Unit       string  `json:"unit"`
	Source     string  `json:"source"`
	VerifiedAt *string `json:"verified_at"`
}
type Capability struct {
	ID                     string              `json:"id"`
	Implementation         string              `json:"implementation"`
	Availability           string              `json:"availability"`
	Enabled                bool                `json:"enabled"`
	Reason                 *string             `json:"reason"`
	RequiredCredentials    []string            `json:"required_credentials"`
	CredentialAlternatives [][]string          `json:"credential_alternatives,omitempty"`
	SupportedOptions       []string            `json:"supported_options"`
	SupportedValues        map[string][]string `json:"supported_option_values,omitempty"`
	Limits                 []Limit             `json:"limits"`
	VerifiedAt             *string             `json:"verified_at"`
}
