package curator

import (
	"encoding/json"
	"sort"
	"time"

	"imbue/internal/evidence"
)

// ModelInput preserves selected text after redaction and explicit image exclusion. It does not
// summarize payloads or infer causal links between adjacent conversations.
type ModelInput struct {
	Format             string         `json:"format"`
	JobID              string         `json:"job_id"`
	Repository         string         `json:"repository"`
	PolicyVersion      string         `json:"policy_version"`
	SchemaVersion      string         `json:"schema_version"`
	Coverage           InputCoverage  `json:"coverage"`
	Timeline           []RawStep      `json:"timeline"`
	ExistingCases      []ExistingCase `json:"existing_cases"`
	AllowedEvidenceIDs []string       `json:"allowed_evidence_ids"`
}
type InputCoverage struct {
	Part          int      `json:"part"`
	Parts         int      `json:"parts"`
	FromSeq       int64    `json:"from_seq"`
	ThroughSeq    int64    `json:"through_seq"`
	ExcludedKinds []string `json:"excluded_kinds"`
	Limitations   []string `json:"limitations"`
}
type RawStep struct {
	Order          int             `json:"order"`
	Origin         string          `json:"origin"`
	EvidenceID     string          `json:"evidence_id"`
	ConversationID string          `json:"conversation_id"`
	TurnID         string          `json:"turn_id"`
	OccurredAt     string          `json:"occurred_at"`
	SourceOffset   int64           `json:"source_offset"`
	Kind           string          `json:"kind"`
	Raw            json.RawMessage `json:"raw"`
}

func modelInput(p Packet) ModelInput {
	m := ModelInput{
		Format: "imbue-raw-timeline-2", JobID: p.JobID, Repository: p.Repository,
		PolicyVersion: p.PolicyVersion, SchemaVersion: p.SchemaVersion,
		Coverage: InputCoverage{Part: p.Part, Parts: p.Parts, FromSeq: p.FromSeq, ThroughSeq: p.ThroughSeq,
			ExcludedKinds: []string{"tool.command", "image.content"},
			Limitations: []string{
				"コマンドと実行出力は除外されている。Agentの実行報告を検証済み事実とみなさない。",
				"画像本体は除外。画像の内容を認識・検証したと解釈しない。",
				"この入力は対象範囲の一部。補助文脈は会話の発言に限定し、過去の全ツール結果は再掲しない。",
				"同じリポジトリの別会話も含み得る。時系列の隣接は因果関係を意味しない。",
				"fragment_index / fragment_countを持つrawは原文JSONの断片。欠損や除外の内容を推測しない。",
			}},
		Timeline: []RawStep{}, ExistingCases: p.ExistingCases, AllowedEvidenceIDs: p.AllowedEvidenceIDs,
	}
	if m.ExistingCases == nil {
		m.ExistingCases = []ExistingCase{}
	}
	if m.AllowedEvidenceIDs == nil {
		m.AllowedEvidenceIDs = []string{}
	}
	selected := map[string]bool{}
	appendStep := func(e evidence.Event, origin string) {
		m.Timeline = append(m.Timeline, RawStep{Origin: origin, EvidenceID: e.ID, ConversationID: e.ConversationID, TurnID: e.TurnID, OccurredAt: e.OccurredAt, SourceOffset: e.SourceOffset, Kind: e.Kind, Raw: e.Payload})
	}
	for _, e := range p.Events {
		selected[e.ID] = true
		appendStep(e, "selected")
	}
	for _, e := range p.ContextEvents {
		if !selected[e.ID] {
			appendStep(e, "context")
		}
	}
	// Compare instants rather than timestamp text (offsets may differ). A missing
	// timestamp retains packet order; no time is fabricated.
	validTimes := true
	times := map[string]time.Time{}
	for _, s := range m.Timeline {
		t, err := time.Parse(time.RFC3339Nano, s.OccurredAt)
		if err != nil {
			validTimes = false
			break
		}
		times[s.OccurredAt] = t
	}
	if validTimes {
		sort.SliceStable(m.Timeline, func(i, j int) bool {
			a, b := m.Timeline[i], m.Timeline[j]
			ta, tb := times[a.OccurredAt], times[b.OccurredAt]
			if !ta.Equal(tb) {
				return ta.Before(tb)
			}
			if a.ConversationID == b.ConversationID {
				return a.SourceOffset < b.SourceOffset
			}
			return a.ConversationID < b.ConversationID
		})
	} else {
		m.Coverage.Limitations = append(m.Coverage.Limitations, "時刻が不明なイベントを含むため配列は入力の列挙順。前後関係は会話・ターン・元位置と確認し、時刻を補完しない。")
	}
	for i := range m.Timeline {
		m.Timeline[i].Order = i + 1
	}
	return m
}
