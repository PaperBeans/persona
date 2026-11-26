package assistantwire

// CapabilityHealthTell belongs to the core, including builds without the Health data module.
const CapabilityHealthTell = "health_tell"

const (
	HealthTellReceived  = "received"
	HealthTellDelivered = "delivered"
	HealthTellFailed    = "failed"
)

type HealthTell struct {
	ClientID string `json:"client_id"`
	Text     string `json:"text"`
}

type HealthTellReceipt struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Duplicate bool   `json:"duplicate,omitempty"`
}

func init() {
	Routes = append(Routes, "POST /api/v1/assistant/health/tell")
}
