package curator

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"imbue/internal/evidence"
)

const Model = "gpt-5.6-luna"
const PolicyVersion = "imbue-curator-4"
const SchemaVersion = "learning-case-patch-2"

type Observation struct {
	Fact     string   `json:"fact"`
	EventIDs []string `json:"event_ids"`
}

type Experience struct {
	Situation                  string        `json:"situation"`
	Observations               []Observation `json:"observations"`
	Hypothesis                 string        `json:"hypothesis"`
	AlternativeInterpretations []string      `json:"alternative_interpretations"`
	Unknowns                   []string      `json:"unknowns"`
}

type Changes struct {
	Experience            *Experience `json:"experience,omitempty"`
	Title                 string      `json:"title"`
	Relation              string      `json:"relation"`
	Repository            string      `json:"repository"`
	Judgment              string      `json:"judgment"`
	Conditions            []string    `json:"conditions"`
	Exceptions            []string    `json:"exceptions"`
	EligibilityCandidates []string    `json:"eligibility_candidates"`
	Status                string      `json:"status"`
}
type Patch struct {
	CaseID             string   `json:"case_id"`
	BaseVersion        int      `json:"base_version"`
	Operation          string   `json:"operation"`
	Changes            Changes  `json:"changes"`
	GroundedInEventIDs []string `json:"grounded_in_event_ids"`
}
type Result struct {
	Patches []Patch `json:"patches"`
}
type ExistingCase struct {
	ID      string          `json:"id"`
	Version int             `json:"version"`
	Status  string          `json:"status"`
	Body    json.RawMessage `json:"body"`
}
type Packet struct {
	JobID              string           `json:"job_id"`
	ConversationID     string           `json:"conversation_id"`
	Repository         string           `json:"repository"`
	PolicyVersion      string           `json:"policy_version"`
	SchemaVersion      string           `json:"schema_version"`
	FromSeq            int64            `json:"from_seq"`
	ThroughSeq         int64            `json:"through_seq"`
	Events             []evidence.Event `json:"events"`
	ContextEvents      []evidence.Event `json:"context_events"`
	ExistingCases      []ExistingCase   `json:"existing_cases"`
	AllowedEvidenceIDs []string         `json:"allowed_evidence_ids"`
	Part               int              `json:"part"`
	Parts              int              `json:"parts"`
}

func Validate(result Result, p Packet) error {
	allowed := map[string]bool{}
	users := map[string]bool{}
	for _, ev := range append(append([]evidence.Event{}, p.Events...), p.ContextEvents...) {
		if ev.Kind == "user.message" {
			users[ev.ID] = true
		}
	}
	for _, id := range p.AllowedEvidenceIDs {
		allowed[id] = true
	}
	versions := map[string]int{}
	for _, c := range p.ExistingCases {
		versions[c.ID] = c.Version
	}
	seen := map[string]bool{}
	for _, v := range result.Patches {
		if v.Changes.Conditions == nil || v.Changes.Exceptions == nil || v.Changes.EligibilityCandidates == nil {
			return fmt.Errorf("required array field is missing")
		}
		if strings.TrimSpace(v.Changes.Title) == "" || strings.TrimSpace(v.Changes.Judgment) == "" || v.Changes.Repository != p.Repository {
			return fmt.Errorf("invalid title, judgment or repository scope")
		}
		if !oneOf(v.Changes.Relation, "correction", "preference_refinement", "requirement_addition", "exploration", "approval", "delegation", "change_of_mind", "bug_report") {
			return fmt.Errorf("unknown relation")
		}
		if !oneOf(v.Changes.Status, "active", "held") {
			return fmt.Errorf("invalid case status")
		}
		if len(v.GroundedInEventIDs) == 0 {
			return fmt.Errorf("case has no evidence")
		}
		userGrounded := false
		for _, id := range v.GroundedInEventIDs {
			if !allowed[id] {
				return fmt.Errorf("unknown evidence reference: %s", id)
			}
			userGrounded = userGrounded || users[id]
		}
		if !userGrounded {
			return fmt.Errorf("judgment must cite an observed user message")
		}
		if p.SchemaVersion == SchemaVersion || v.Changes.Experience != nil {
			x := v.Changes.Experience
			if x == nil || strings.TrimSpace(x.Situation) == "" || len(x.Observations) == 0 || x.AlternativeInterpretations == nil || x.Unknowns == nil {
				return fmt.Errorf("grounded experience is required")
			}
			grounds := map[string]bool{}
			for _, id := range v.GroundedInEventIDs {
				grounds[id] = true
			}
			for _, o := range x.Observations {
				if strings.TrimSpace(o.Fact) == "" || len(o.EventIDs) == 0 {
					return fmt.Errorf("observation requires fact and evidence")
				}
				for _, id := range o.EventIDs {
					if !allowed[id] || !grounds[id] {
						return fmt.Errorf("observation cites ungrounded evidence: %s", id)
					}
				}
			}
		}
		for _, purpose := range v.Changes.EligibilityCandidates {
			if !oneOf(purpose, "SFT", "refinement_SFT", "DPO", "RFT", "Judge", "Eval", "Skill", "Memory") {
				return fmt.Errorf("invalid eligibility candidate")
			}
		}
		switch v.Operation {
		case "create":
			if v.CaseID != "" || v.BaseVersion != 0 {
				return fmt.Errorf("create must have empty case_id and base_version=0")
			}
		case "update":
			if ver, ok := versions[v.CaseID]; !ok || ver != v.BaseVersion {
				return fmt.Errorf("stale or unknown case version")
			}
			if seen[v.CaseID] {
				return fmt.Errorf("multiple updates to one case")
			}
			seen[v.CaseID] = true
		default:
			return fmt.Errorf("invalid patch operation")
		}
	}
	return nil
}
func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

//go:embed instructions_ja.md
var Instructions string

const Schema = `{
 "type":"object","additionalProperties":false,"required":["patches"],"properties":{
 "patches":{"type":"array","items":{"type":"object","additionalProperties":false,
 "required":["case_id","base_version","operation","changes","grounded_in_event_ids"],"properties":{
 "case_id":{"type":"string"},"base_version":{"type":"integer"},"operation":{"type":"string","enum":["create","update"]},
 "grounded_in_event_ids":{"type":"array","items":{"type":"string","pattern":"^E[0-9]{6}$"}},
 "changes":{"type":"object","additionalProperties":false,"required":["title","relation","repository","judgment","conditions","exceptions","eligibility_candidates","status","experience"],"properties":{
 "experience":{"type":"object","additionalProperties":false,"required":["situation","observations","hypothesis","alternative_interpretations","unknowns"],"properties":{
 "situation":{"type":"string"},"hypothesis":{"type":"string"},
 "alternative_interpretations":{"type":"array","items":{"type":"string"}},"unknowns":{"type":"array","items":{"type":"string"}},
 "observations":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["fact","event_ids"],"properties":{"fact":{"type":"string"},"event_ids":{"type":"array","items":{"type":"string","pattern":"^E[0-9]{6}$"}}}}}}},
 "title":{"type":"string"},"relation":{"type":"string","enum":["correction","preference_refinement","requirement_addition","exploration","approval","delegation","change_of_mind","bug_report"]},
 "repository":{"type":"string"},"judgment":{"type":"string"},"conditions":{"type":"array","items":{"type":"string"}},"exceptions":{"type":"array","items":{"type":"string"}},
 "eligibility_candidates":{"type":"array","items":{"type":"string","enum":["SFT","refinement_SFT","DPO","RFT","Judge","Eval","Skill","Memory"]}},"status":{"type":"string","enum":["active","held"]}
 }}}}}}}
`
