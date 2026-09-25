-- name: UpsertConversation :exec
INSERT INTO conversations(id, repository, source_path, codex_version) VALUES ($1,$2,$3,$4)
ON CONFLICT(id) DO NOTHING;

-- name: InsertEvidence :one
INSERT INTO evidence(id,conversation_id,turn_id,kind,occurred_at,source_offset,blob_hash,byte_count)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO UPDATE SET id=EXCLUDED.id RETURNING seq;

-- name: ListConversations :many
SELECT c.*, count(t.id)::bigint AS completed_turns,
 count(t.id) FILTER (WHERE t.completion_seq > c.processed_seq)::bigint AS pending_turns
FROM conversations c LEFT JOIN turns t ON t.conversation_id=c.id AND t.status='completed'
GROUP BY c.id ORDER BY c.created_at;

-- name: ListEvidence :many
SELECT * FROM evidence WHERE conversation_id=$1 ORDER BY seq LIMIT $2;

-- name: GetEvidence :one
SELECT * FROM evidence WHERE id=$1;

-- name: GetConversation :one
SELECT * FROM conversations WHERE id=$1;

-- name: StartTurn :exec
INSERT INTO turns(conversation_id,id,start_evidence_id,status) VALUES($1,$2,$3,'started')
ON CONFLICT(conversation_id,id) DO UPDATE SET start_evidence_id=COALESCE(turns.start_evidence_id,EXCLUDED.start_evidence_id);

-- name: CompleteTurn :exec
INSERT INTO turns(conversation_id,id,complete_evidence_id,completion_seq,status) VALUES($1,$2,$3,$4,'completed')
ON CONFLICT(conversation_id,id) DO UPDATE SET complete_evidence_id=COALESCE(turns.complete_evidence_id,EXCLUDED.complete_evidence_id),
completion_seq=COALESCE(turns.completion_seq,EXCLUDED.completion_seq),status='completed';

-- name: AbortTurn :exec
INSERT INTO turns(conversation_id,id,status) VALUES($1,$2,'aborted')
ON CONFLICT(conversation_id,id) DO UPDATE SET status=CASE WHEN turns.status='completed' THEN 'completed' ELSE 'aborted' END;

-- name: ListTurns :many
SELECT * FROM turns WHERE conversation_id=$1 ORDER BY completion_seq NULLS LAST, id;

-- name: ListJobs :many
SELECT * FROM jobs ORDER BY created_at DESC LIMIT 100;

-- name: ListCases :many
SELECT * FROM learning_cases ORDER BY updated_at DESC LIMIT 100;

-- name: GetCase :one
SELECT * FROM learning_cases WHERE id=$1;

-- name: CaseHistory :many
SELECT * FROM case_versions WHERE case_id=$1 ORDER BY version;

-- name: ClaimJob :one
UPDATE jobs SET status='running', attempt=attempt+1,
 lease_until=now()+sqlc.arg(lease_seconds)::integer * interval '1 second', lease_token=sqlc.arg(token)::text
WHERE id=(SELECT id FROM jobs WHERE
 ((status IN ('queued','retry','blocked_by_codex') AND next_run_at<=now()) OR (status='running' AND lease_until<now()))
 AND attempt < sqlc.arg(max_attempts)::integer
 ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
RETURNING *;

-- name: FailJob :exec
UPDATE jobs SET status=$3,error=$4,next_run_at=now()+interval '60 seconds',lease_until=NULL,lease_token=NULL
WHERE id=$1 AND lease_token=$2;

-- name: SetPacket :execrows
UPDATE jobs SET packet_hash=$3 WHERE id=$1 AND lease_token=$2;

-- name: RetryJob :execrows
UPDATE jobs SET status='queued', attempt=0, error='', next_run_at=now(), lease_until=NULL, lease_token=NULL
WHERE id=$1 AND status NOT IN ('running','succeeded');
