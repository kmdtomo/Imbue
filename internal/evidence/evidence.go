package evidence

import "encoding/json"

type Conversation struct {
	ID           string `json:"id"`
	Repository   string `json:"repository"`
	SourcePath   string `json:"source_path"`
	CodexVersion string `json:"codex_version"`
}
type Event struct {
	ID             string          `json:"id"`
	ConversationID string          `json:"conversation_id"`
	TurnID         string          `json:"turn_id"`
	Kind           string          `json:"kind"`
	OccurredAt     string          `json:"occurred_at"`
	SourceOffset   int64           `json:"source_offset"`
	Payload        json.RawMessage `json:"payload"`
}
type Batch struct {
	Conversation Conversation `json:"conversation"`
	Events       []Event      `json:"events"`
}
