package store

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"imbue/internal/config"
	"imbue/internal/evidence"
	"imbue/internal/local"
	"imbue/internal/store/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	Pool *pgxpool.Pool
	Q    *db.Queries
	Root string
}

func Open(ctx context.Context, c config.Config) (*Store, error) {
	dsn, e := c.DSN()
	if e != nil {
		return nil, e
	}
	pc, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		return nil, e
	}
	pc.ConnConfig.ConnectTimeout = 3 * time.Second
	pc.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	pc.MaxConns = 5
	p, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, fmt.Errorf("PostgreSQL unavailable: %w", e)
	}
	return &Store{p, db.New(p), c.Root}, nil
}
func (s *Store) Close() { s.Pool.Close() }
func (s *Store) Migrate(ctx context.Context) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(70410318)"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"); e != nil {
		return e
	}
	var exists bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=1)").Scan(&exists); e != nil {
		return e
	}
	if !exists {
		b, _ := migrations.ReadFile("migrations/001_initial.sql")
		if _, e = tx.Exec(ctx, string(b)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES(1)"); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) Ingest(ctx context.Context, b evidence.Batch) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	q := s.Q.WithTx(tx)
	if e = q.UpsertConversation(ctx, db.UpsertConversationParams{ID: b.Conversation.ID, Repository: b.Conversation.Repository, SourcePath: b.Conversation.SourcePath, CodexVersion: b.Conversation.CodexVersion}); e != nil {
		return e
	}
	for _, v := range b.Events {
		body, e := json.Marshal(v)
		if e != nil {
			return e
		}
		h, e := local.PutBlob(s.Root, body)
		if e != nil {
			return e
		}
		seq, e := q.InsertEvidence(ctx, db.InsertEvidenceParams{ID: v.ID, ConversationID: v.ConversationID, TurnID: v.TurnID, Kind: v.Kind, OccurredAt: v.OccurredAt, SourceOffset: v.SourceOffset, BlobHash: h, ByteCount: int64(len(body))})
		if e != nil {
			return e
		}
		switch v.Kind {
		case "turn.started":
			if v.TurnID != "" {
				e = q.StartTurn(ctx, db.StartTurnParams{ConversationID: v.ConversationID, ID: v.TurnID, StartEvidenceID: pgtype.Text{String: v.ID, Valid: true}})
			}
		case "turn.completed":
			if v.TurnID != "" {
				e = q.CompleteTurn(ctx, db.CompleteTurnParams{ConversationID: v.ConversationID, ID: v.TurnID, CompleteEvidenceID: pgtype.Text{String: v.ID, Valid: true}, CompletionSeq: pgtype.Int8{Int64: seq, Valid: true}})
			}
		case "turn.aborted":
			if v.TurnID != "" {
				e = q.AbortTurn(ctx, db.AbortTurnParams{ConversationID: v.ConversationID, ID: v.TurnID})
			}
		}
		if e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
