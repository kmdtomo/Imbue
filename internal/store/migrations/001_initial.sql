CREATE TABLE IF NOT EXISTS schema_migrations (version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE conversations (
 id text PRIMARY KEY, repository text NOT NULL, source_path text NOT NULL,
 codex_version text NOT NULL, processed_seq bigint NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE evidence (
 seq bigserial PRIMARY KEY, id text UNIQUE NOT NULL,
 conversation_id text NOT NULL REFERENCES conversations(id), turn_id text NOT NULL,
 kind text NOT NULL, occurred_at text NOT NULL, source_offset bigint NOT NULL,
 blob_hash text NOT NULL, byte_count bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX evidence_conversation ON evidence(conversation_id, seq);
CREATE INDEX evidence_turn ON evidence(conversation_id, turn_id);
CREATE TABLE turns (
 conversation_id text NOT NULL REFERENCES conversations(id), id text NOT NULL,
 start_evidence_id text REFERENCES evidence(id), complete_evidence_id text REFERENCES evidence(id),
 completion_seq bigint UNIQUE, status text NOT NULL,
 PRIMARY KEY(conversation_id,id)
);
CREATE TABLE jobs (
 id text PRIMARY KEY, conversation_id text NOT NULL REFERENCES conversations(id),
 from_seq bigint NOT NULL, through_seq bigint NOT NULL,
 status text NOT NULL DEFAULT 'queued', attempt integer NOT NULL DEFAULT 0,
 next_run_at timestamptz NOT NULL DEFAULT now(), lease_until timestamptz,
 lease_token text, error text NOT NULL DEFAULT '', packet_hash text,
 result_hash text, created_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz
);
CREATE UNIQUE INDEX jobs_conversation_active ON jobs(conversation_id) WHERE status <> 'succeeded';
CREATE TABLE learning_cases (
 id text PRIMARY KEY, conversation_id text NOT NULL REFERENCES conversations(id),
 version integer NOT NULL, status text NOT NULL, body jsonb NOT NULL,
 job_id text NOT NULL REFERENCES jobs(id), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE case_versions (
 case_id text NOT NULL REFERENCES learning_cases(id), version integer NOT NULL,
 body jsonb NOT NULL, status text NOT NULL, actor text NOT NULL,
 reason text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(case_id, version)
);
