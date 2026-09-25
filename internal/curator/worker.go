package curator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"imbue/internal/config"
	"imbue/internal/evidence"
	"imbue/internal/local"
	"imbue/internal/redact"
	"imbue/internal/store"
	"imbue/internal/store/db"
)

type Worker struct {
	Config config.Config
	Store  *store.Store
	Runner Runner
}

// includedInCuration separates retained audit evidence from Luna's learning
// input. Commands and their output stay in the evidence store but never cross
// the model boundary.
func includedInCuration(ev evidence.Event) bool {
	return ev.Kind != "tool.command"
}

func (w *Worker) Allowed(id, repository string) bool {
	if !w.Config.ObserveAll && repository != w.Config.Repository {
		return false
	}
	for _, excluded := range w.Config.ExcludeSessions {
		if id == excluded {
			return false
		}
	}
	return true
}

func (w *Worker) Enqueue(ctx context.Context, conversation string, manual bool) (string, error) {
	tx, e := w.Store.Pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	var repository string
	if e = tx.QueryRow(ctx, "SELECT repository FROM conversations WHERE id=$1", conversation).Scan(&repository); e != nil {
		return "", e
	}
	if !w.Allowed(conversation, repository) {
		return "", fmt.Errorf("conversation is outside the configured curation scope")
	}
	rows, e := tx.Query(ctx, "SELECT id,processed_seq FROM conversations WHERE repository=$1 ORDER BY id FOR UPDATE", repository)
	if e != nil {
		return "", e
	}
	ids := []string{}
	var from int64
	first := true
	for rows.Next() {
		var id string
		var processed int64
		if e = rows.Scan(&id, &processed); e != nil {
			rows.Close()
			return "", e
		}
		if !w.Allowed(id, repository) {
			continue
		}
		ids = append(ids, id)
		if first || processed < from {
			from = processed
			first = false
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return "", e
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("repository has no conversations in the configured curation scope")
	}
	var existing string
	e = tx.QueryRow(ctx, "SELECT id FROM jobs WHERE conversation_id=ANY($1) AND status<>'succeeded' LIMIT 1", ids).Scan(&existing)
	if e == nil {
		return existing, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return "", e
	}
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM turns t JOIN conversations c ON c.id=t.conversation_id
WHERE t.conversation_id=ANY($1) AND t.status='completed' AND t.completion_seq>c.processed_seq`, ids).Scan(&count); e != nil {
		return "", e
	}
	if !manual && count < w.Config.CuratorThreshold {
		return "", nil
	}
	// Include late evidence belonging to completed turns, but never unfinished turns.
	var through int64
	if e = tx.QueryRow(ctx, `SELECT COALESCE(max(e.seq),0) FROM evidence e
JOIN turns t ON t.conversation_id=e.conversation_id AND t.id=e.turn_id
JOIN conversations c ON c.id=t.conversation_id
WHERE e.conversation_id=ANY($1) AND t.status='completed' AND t.completion_seq>c.processed_seq`, ids).Scan(&through); e != nil {
		return "", e
	}
	if through <= from {
		return "", nil
	}
	id := local.Hash([]byte(fmt.Sprintf("%s:%d:%d:%s", repository, from, through, PolicyVersion)))[:32]
	if _, e = tx.Exec(ctx, "INSERT INTO jobs(id,conversation_id,from_seq,through_seq) VALUES($1,$2,$3,$4)", id, ids[0], from, through); e != nil {
		return "", e
	}
	return id, tx.Commit(ctx)
}
func (w *Worker) EnqueueReady(ctx context.Context) error {
	if !w.Config.CuratorEnabled {
		return nil
	}
	cs, e := w.Store.Q.ListConversations(ctx)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, c := range cs {
		if !w.Allowed(c.ID, c.Repository) {
			continue
		}
		if seen[c.Repository] {
			continue
		}
		seen[c.Repository] = true
		if _, e = w.Enqueue(ctx, c.ID, false); e != nil {
			return e
		}
	}
	return nil
}
func (w *Worker) Packet(ctx context.Context, j db.Job) (Packet, error) {
	c, e := w.Store.Q.GetConversation(ctx, j.ConversationID)
	if e != nil {
		return Packet{}, e
	}
	if !w.Allowed(c.ID, c.Repository) {
		return Packet{}, fmt.Errorf("conversation is outside the configured curation scope")
	}
	rows, e := w.Store.Pool.Query(ctx, "SELECT id FROM conversations WHERE repository=$1 ORDER BY id", c.Repository)
	if e != nil {
		return Packet{}, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return Packet{}, e
		}
		if w.Allowed(id, c.Repository) {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return Packet{}, e
	}
	red, e := redact.New(w.Config.RedactPatterns, w.Config.ExcludePaths)
	if e != nil {
		return Packet{}, e
	}
	p := Packet{JobID: j.ID, ConversationID: j.ConversationID, Repository: c.Repository, PolicyVersion: PolicyVersion, SchemaVersion: SchemaVersion, FromSeq: j.FromSeq, ThroughSeq: j.ThroughSeq, Events: []evidence.Event{}, ExistingCases: []ExistingCase{}, AllowedEvidenceIDs: []string{}}
	// Include the selected turns plus the two immediately preceding completed turns.
	rows, e = w.Store.Pool.Query(ctx, `WITH selected AS (
 SELECT t.conversation_id,t.id FROM turns t JOIN conversations c ON c.id=t.conversation_id
 WHERE t.conversation_id=ANY($1) AND t.status='completed' AND t.completion_seq>c.processed_seq AND t.completion_seq<=$2
 ), context_turns AS (
 SELECT conversation_id,id FROM (
 SELECT t.conversation_id,t.id,row_number() OVER (PARTITION BY t.conversation_id ORDER BY t.completion_seq DESC) AS position
 FROM turns t JOIN conversations c ON c.id=t.conversation_id
 WHERE t.conversation_id=ANY($1) AND t.status='completed' AND t.completion_seq<=c.processed_seq
 ) preceding WHERE position<=2
 )
 SELECT e.blob_hash, ((e.conversation_id,e.turn_id) IN (SELECT conversation_id,id FROM context_turns)) FROM evidence e WHERE e.seq<=$2 AND
 ((e.conversation_id,e.turn_id) IN (SELECT conversation_id,id FROM selected) OR
  (e.conversation_id,e.turn_id) IN (SELECT conversation_id,id FROM context_turns))
 ORDER BY e.occurred_at,e.seq`, ids, j.ThroughSeq)
	if e != nil {
		return p, e
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		var isContext bool
		if e = rows.Scan(&h, &isContext); e != nil {
			return p, e
		}
		b, e := local.ReadBlob(w.Config.Root, h)
		if e != nil {
			return p, e
		}
		var ev evidence.Event
		if e = json.Unmarshal(b, &ev); e != nil {
			return p, e
		}
		if !includedInCuration(ev) {
			continue
		}
		// Re-apply the current policy: exclusions may have changed since capture.
		if red.Excluded(string(ev.Payload)) {
			ev.Kind = "evidence.excluded"
			ev.Payload = json.RawMessage(`{"reason":"excluded by current curation policy"}`)
		} else {
			ev.Payload, e = red.JSON(ev.Payload)
			if e != nil {
				return p, e
			}
		}
		if isContext {
			p.ContextEvents = append(p.ContextEvents, ev)
		} else {
			p.Events = append(p.Events, ev)
		}
		p.AllowedEvidenceIDs = append(p.AllowedEvidenceIDs, ev.ID)
	}
	if e = rows.Err(); e != nil {
		return p, e
	}
	rows.Close()
	cases, e := w.Store.Pool.Query(ctx, "SELECT id,version,status,body FROM learning_cases WHERE conversation_id=ANY($1) ORDER BY id", ids)
	if e != nil {
		return p, e
	}
	defer cases.Close()
	for cases.Next() {
		var c ExistingCase
		if e = cases.Scan(&c.ID, &c.Version, &c.Status, &c.Body); e != nil {
			return p, e
		}
		if red.Excluded(string(c.Body)) {
			continue
		}
		c.Body, e = red.JSON(c.Body)
		if e != nil {
			return p, e
		}
		p.ExistingCases = append(p.ExistingCases, c)
	}
	return p, cases.Err()
}

// Split preserves every evidence payload. Oversized records carry explicit fragments,
// with their original ID/turn and indices; no silent truncation is permitted.
func splitWithFragments(p Packet, limit int) ([]Packet, error) {
	parts, e := splitRaw(p, limit/2)
	if e != nil {
		return nil, e
	}
	type turnKey struct{ conversation, turn string }
	users := map[turnKey]evidence.Event{}
	previous := map[turnKey][]evidence.Event{}
	lastUsers := map[string]evidence.Event{}
	lastAnswers := map[string]evidence.Event{}
	for _, ev := range p.Events {
		key := turnKey{ev.ConversationID, ev.TurnID}
		if _, ok := previous[key]; !ok {
			previous[key] = []evidence.Event{lastUsers[ev.ConversationID], lastAnswers[ev.ConversationID]}
		}
		if ev.Kind == "user.message" {
			users[key] = ev
			lastUsers[ev.ConversationID] = ev
		}
		if ev.Kind == "agent.message" {
			var v struct {
				Phase string `json:"phase"`
			}
			json.Unmarshal(ev.Payload, &v)
			if v.Phase == "final_answer" || v.Phase == "final" || v.Phase == "" {
				lastAnswers[ev.ConversationID] = ev
			}
		}
	}
	for i := range parts {
		seen := map[string]bool{}
		for _, ev := range parts[i].Events {
			seen[ev.ID] = true
		}
		candidates := []evidence.Event{}
		for _, ev := range parts[i].Events {
			candidates = append(candidates, users[turnKey{ev.ConversationID, ev.TurnID}])
			candidates = append(candidates, previous[turnKey{ev.ConversationID, ev.TurnID}]...)
		}
		for _, ev := range candidates {
			if ev.ID == "" || seen[ev.ID] {
				continue
			}
			seen[ev.ID] = true
			// Oversized context is already present in full across its own fragments.
			// A visible reference describes that boundary instead of pretending it was supplied whole.
			if len(ev.Payload) > limit/4 {
				ev.Kind = "context.fragment_reference"
				ev.Payload = json.RawMessage(`{"note":"Related message is distributed across other parts; do not infer its full contents from this reference."}`)
			} else {
				parts[i].AllowedEvidenceIDs = append(parts[i].AllowedEvidenceIDs, ev.ID)
			}
			parts[i].ContextEvents = append(parts[i].ContextEvents, ev)
		}
		b, _ := json.Marshal(modelInput(parts[i]))
		if len(b) > limit {
			return nil, fmt.Errorf("related message context exceeds packet budget; increase curator_max_packet_bytes")
		}
	}
	return parts, nil
}

func splitRaw(p Packet, limit int) ([]Packet, error) {
	base := p
	base.Events = nil
	base.AllowedEvidenceIDs = nil
	base.Part = 99999
	base.Parts = 99999
	size := func(v Packet) int { b, _ := json.Marshal(v); return len(b) }
	if size(base) > limit/2 {
		return nil, fmt.Errorf("existing case context exceeds packet budget; revise scope before retry")
	}
	var events []evidence.Event
	for _, ev := range p.Events {
		tmp := base
		tmp.Events = []evidence.Event{ev}
		tmp.AllowedEvidenceIDs = []string{ev.ID}
		if size(tmp) <= limit {
			events = append(events, ev)
			continue
		}
		// Strings are split on rune boundaries; the original JSON is reconstructed by concatenation.
		runes := []rune(string(ev.Payload))
		chunk := limit / 16
		if chunk < 1 {
			return nil, fmt.Errorf("packet limit too small")
		}
		total := (len(runes) + chunk - 1) / chunk
		for i := 0; i < total; i++ {
			end := (i + 1) * chunk
			if end > len(runes) {
				end = len(runes)
			}
			part := ev
			part.Payload, _ = json.Marshal(map[string]any{"fragment_index": i + 1, "fragment_count": total, "original_payload_json_fragment": string(runes[i*chunk : end])})
			tmp.Events = []evidence.Event{part}
			if size(tmp) > limit {
				return nil, fmt.Errorf("fragment exceeds packet budget")
			}
			events = append(events, part)
		}
	}
	out := []Packet{}
	cur := base
	cur.Events = []evidence.Event{}
	cur.AllowedEvidenceIDs = []string{}
	for _, ev := range events {
		trial := cur
		trial.Events = append(append([]evidence.Event{}, cur.Events...), ev)
		trial.AllowedEvidenceIDs = append(append([]string{}, cur.AllowedEvidenceIDs...), ev.ID)
		if size(trial) > limit && len(cur.Events) > 0 {
			out = append(out, cur)
			cur = base
			cur.Events = []evidence.Event{ev}
			cur.AllowedEvidenceIDs = []string{ev.ID}
		} else {
			cur = trial
		}
	}
	if len(cur.Events) > 0 {
		out = append(out, cur)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no evidence for job")
	}
	for i := range out {
		out[i].Part = i + 1
		out[i].Parts = len(out)
	}
	return out, nil
}
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	// A process that dies during its last allowed attempt must not stay running forever.
	_, e := w.Store.Pool.Exec(ctx, "UPDATE jobs SET status='quarantined',error='lease expired after final attempt',lease_token=NULL,lease_until=NULL WHERE status='running' AND lease_until<now() AND attempt>=$1", w.Config.CuratorMaxAttempts)
	if e != nil {
		return false, e
	}
	job, e := w.Store.Q.ClaimJob(ctx, db.ClaimJobParams{LeaseSeconds: int32(w.Config.CuratorTimeoutSeconds + 30), Token: local.ID(), MaxAttempts: int32(w.Config.CuratorMaxAttempts)})
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-tick.C:
				tag, e := w.Store.Pool.Exec(runCtx, "UPDATE jobs SET lease_until=now()+$3::integer*interval '1 second' WHERE id=$1 AND lease_token=$2 AND status='running'", job.ID, job.LeaseToken.String, w.Config.CuratorTimeoutSeconds+30)
				if e != nil || tag.RowsAffected() != 1 {
					cancel()
					return
				}
			}
		}
	}()
	resultErr := w.process(runCtx, job)
	cancel()
	<-renewDone
	if resultErr != nil {
		status := "retry"
		if strings.Contains(resultErr.Error(), "blocked_by_account") {
			status = "blocked_by_account"
		}
		if strings.Contains(resultErr.Error(), "blocked_by_codex") {
			status = "blocked_by_codex"
		}
		if int(job.Attempt) >= w.Config.CuratorMaxAttempts && status != "blocked_by_account" {
			status = "quarantined"
		}
		failCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		msg := resultErr.Error()
		if len(msg) > 4000 {
			msg = msg[:4000]
		}
		if e = w.Store.Q.FailJob(failCtx, db.FailJobParams{ID: job.ID, LeaseToken: job.LeaseToken, Status: status, Error: msg}); e != nil {
			return true, fmt.Errorf("job failure could not be recorded: %w", e)
		}
	}
	return true, resultErr
}
func (w *Worker) process(ctx context.Context, j db.Job) error {
	p, e := w.Packet(ctx, j)
	if e != nil {
		return e
	}
	parts, e := Split(p, w.Config.CuratorMaxPacketBytes)
	if e != nil {
		return e
	}
	manifest, _ := json.Marshal(parts)
	hash, e := local.PutBlob(w.Config.Root, manifest)
	if e != nil {
		return e
	}
	n, e := w.Store.Q.SetPacket(ctx, db.SetPacketParams{ID: j.ID, LeaseToken: j.LeaseToken, PacketHash: pgtype.Text{String: hash, Valid: true}})
	if e != nil {
		return e
	}
	if n != 1 {
		return fmt.Errorf("job lease lost")
	}
	result := Result{Patches: []Patch{}}
	usage := []json.RawMessage{}
	for _, part := range parts {
		r, u, e := w.Runner.Run(ctx, part)
		if e != nil {
			return e
		}
		if e = Validate(r, part); e != nil {
			return e
		}
		result.Patches = append(result.Patches, r.Patches...)
		if len(u) > 0 {
			usage = append(usage, u)
		}
	}
	// Independent parts may propose the exact same case. Collapse only exact duplicates.
	seen := map[string]bool{}
	unique := []Patch{}
	for _, patch := range result.Patches {
		sort.Strings(patch.GroundedInEventIDs)
		b, _ := json.Marshal(patch)
		h := local.Hash(b)
		if !seen[h] {
			unique = append(unique, patch)
			seen[h] = true
		}
	}
	result.Patches = unique
	if e = Validate(result, p); e != nil {
		return e
	}
	audit, _ := json.Marshal(map[string]any{"model": Model, "policy_version": PolicyVersion, "schema_version": SchemaVersion, "packet_hash": hash, "result": result, "usage": usage})
	rh, e := local.PutBlob(w.Config.Root, audit)
	if e != nil {
		return e
	}
	return w.commit(ctx, j, p, result, rh)
}
func (w *Worker) commit(ctx context.Context, j db.Job, p Packet, r Result, resultHash string) error {
	tx, e := w.Store.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var token, status string
	if e = tx.QueryRow(ctx, "SELECT COALESCE(lease_token,''),status FROM jobs WHERE id=$1 FOR UPDATE", j.ID).Scan(&token, &status); e != nil {
		return e
	}
	if token != j.LeaseToken.String || status != "running" {
		return fmt.Errorf("job lease lost")
	}
	var repository string
	if e = tx.QueryRow(ctx, "SELECT repository FROM conversations WHERE id=$1", j.ConversationID).Scan(&repository); e != nil {
		return e
	}
	rows, e := tx.Query(ctx, "SELECT id,processed_seq FROM conversations WHERE repository=$1 ORDER BY id FOR UPDATE", repository)
	if e != nil {
		return e
	}
	ids := []string{}
	var from int64
	first := true
	for rows.Next() {
		var id string
		var processed int64
		if e = rows.Scan(&id, &processed); e != nil {
			rows.Close()
			return e
		}
		if !w.Allowed(id, repository) {
			continue
		}
		ids = append(ids, id)
		if first || processed < from {
			from = processed
			first = false
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	if from != j.FromSeq {
		return fmt.Errorf("stale processed position")
	}
	for i, patch := range r.Patches {
		id := patch.CaseID
		version := patch.BaseVersion + 1
		if patch.Operation == "create" {
			id = local.Hash([]byte(fmt.Sprintf("%s:case:%d", j.ID, i)))[:32]
			version = 1
		} else {
			var v int
			var status string
			if e = tx.QueryRow(ctx, "SELECT version,status FROM learning_cases WHERE id=$1 AND conversation_id=ANY($2) FOR UPDATE", id, ids).Scan(&v, &status); e != nil {
				return e
			}
			if v != patch.BaseVersion {
				return fmt.Errorf("case changed while curator was running")
			}
			if status == "held" {
				patch.Changes.Status = "held"
			}
		}
		body, _ := json.Marshal(patch)
		if patch.Operation == "create" {
			_, e = tx.Exec(ctx, "INSERT INTO learning_cases(id,conversation_id,version,status,body,job_id) VALUES($1,$2,$3,$4,$5,$6)", id, j.ConversationID, version, patch.Changes.Status, body, j.ID)
		} else {
			_, e = tx.Exec(ctx, "UPDATE learning_cases SET version=$2,status=$3,body=$4,job_id=$5,updated_at=now() WHERE id=$1", id, version, patch.Changes.Status, body, j.ID)
		}
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO case_versions(case_id,version,body,status,actor,reason) VALUES($1,$2,$3,$4,'luna','curator annotation; eligibility is unverified')", id, version, body, patch.Changes.Status); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, "UPDATE conversations SET processed_seq=GREATEST(processed_seq,$2) WHERE id=ANY($1)", ids, j.ThroughSeq); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE jobs SET status='succeeded',result_hash=$2,completed_at=now(),error='',lease_until=NULL,lease_token=NULL WHERE id=$1", j.ID, resultHash); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type Edit struct {
	BaseVersion int    `json:"base_version"`
	Judgment    string `json:"judgment"`
	Status      string `json:"status"`
	Reason      string `json:"reason"`
}

func (w *Worker) EditCase(ctx context.Context, id string, v Edit) error {
	if v.BaseVersion < 1 || strings.TrimSpace(v.Reason) == "" || !oneOf(v.Status, "active", "held") {
		return fmt.Errorf("base_version, reason and active/held status required")
	}
	tx, e := w.Store.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var body []byte
	var version int
	if e = tx.QueryRow(ctx, "SELECT version,body FROM learning_cases WHERE id=$1 FOR UPDATE", id).Scan(&version, &body); e != nil {
		return e
	}
	if version != v.BaseVersion {
		return fmt.Errorf("case version conflict")
	}
	var patch Patch
	if e = json.Unmarshal(body, &patch); e != nil {
		return e
	}
	if v.Judgment != "" {
		patch.Changes.Judgment = v.Judgment
	}
	patch.Changes.Status = v.Status
	body, _ = json.Marshal(patch)
	red, e := redact.New(w.Config.RedactPatterns, w.Config.ExcludePaths)
	if e != nil {
		return e
	}
	body, e = red.JSON(body)
	if e != nil {
		return e
	}
	reason := red.Text(v.Reason)
	if _, e = tx.Exec(ctx, "UPDATE learning_cases SET version=version+1,body=$2,status=$3,updated_at=now() WHERE id=$1", id, body, v.Status); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO case_versions(case_id,version,body,status,actor,reason) VALUES($1,$2,$3,$4,'user',$5)", id, version+1, body, v.Status, reason); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
