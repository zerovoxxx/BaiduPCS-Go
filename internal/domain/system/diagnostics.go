package system

type NetworkDiagnostics struct {
	LocalAddresses []string `json:"local_addresses"`
	Outbound       *string  `json:"outbound_address"`
	Active         bool     `json:"active_probe"`
	Checked        string   `json:"checked_at"`
}
