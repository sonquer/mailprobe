package smtp

const (
	ResultDeliverable   = "deliverable"
	ResultUndeliverable = "undeliverable"
	ResultCatchAll      = "catch_all"
	ResultUnknown       = "unknown"
	ResultNoMX          = "no_mx"
)

type VerifyResult struct {
	Email      string `json:"email"`
	Result     string `json:"result"`
	MX         string `json:"mx"`
	SMTPCode   int    `json:"smtp_code"`
	CatchAll   bool   `json:"catch_all"`
	DurationMs int64  `json:"duration_ms"`
}

type BatchVerifyResponse struct {
	Results         []VerifyResult `json:"results"`
	Domain          string         `json:"domain"`
	MX              string         `json:"mx"`
	CatchAll        bool           `json:"catch_all"`
	TotalDurationMs int64          `json:"total_duration_ms"`
}
