package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"imbue/internal/collect"
	"imbue/internal/config"
	"imbue/internal/curator"
	"imbue/internal/local"
	"imbue/internal/store"
	"imbue/internal/store/db"
)

type Server struct {
	Config    config.Config
	Store     *store.Store
	Collector *collect.Collector
	mu        sync.Mutex
	LastScan  collect.Report
	LastError string
	Cancel    context.CancelFunc
	Worker    *curator.Worker
}

func JSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func Error(w http.ResponseWriter, e error) { http.Error(w, e.Error(), http.StatusBadRequest) }
func (s *Server) Collect(ctx context.Context) (collect.Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, e := s.Collector.Scan(ctx)
	s.LastScan = r
	if e == nil {
		_, e = collect.Flush(ctx, s.Config.Root, s.Store)
	}
	s.LastError = ""
	if e != nil {
		s.LastError = e.Error()
	}
	return r, e
}
func (s *Server) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		cs, e := s.Store.Q.ListConversations(r.Context())
		if e != nil {
			Error(w, e)
			return
		}
		sp, _ := filepath.Glob(filepath.Join(s.Config.Root, "spool", "events", "*.json"))
		s.mu.Lock()
		defer s.mu.Unlock()
		JSON(w, map[string]any{"running": true, "pid": os.Getpid(), "repository": s.Config.Repository, "observe_all": s.Config.ObserveAll, "observe_all_since": s.Config.ObserveAllSince, "curator_enabled": s.Config.CuratorEnabled, "curator_threshold": s.Config.CuratorThreshold, "curator_account_email": s.Config.CuratorAccountEmail, "curator_codex_home": s.Config.CuratorCodexHome, "conversations": cs, "buffered_batches": len(sp), "last_scan": s.LastScan, "last_error": s.LastError})
	})
	mux.HandleFunc("POST /collect", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Collect(r.Context())
		if e != nil {
			Error(w, e)
			return
		}
		JSON(w, v)
	})
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) { JSON(w, map[string]bool{"stopping": true}); s.Cancel() })
	mux.HandleFunc("GET /conversations", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Store.Q.ListConversations(r.Context())
		if e != nil {
			Error(w, e)
			return
		}
		JSON(w, v)
	})
	mux.HandleFunc("GET /turns/{conversation}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Store.Q.ListTurns(r.Context(), r.PathValue("conversation"))
		if e != nil {
			Error(w, e)
			return
		}
		JSON(w, v)
	})
	mux.HandleFunc("GET /evidence", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Store.Q.ListEvidence(r.Context(), db.ListEvidenceParams{ConversationID: r.URL.Query().Get("conversation"), Limit: 1000})
		if e != nil {
			Error(w, e)
			return
		}
		JSON(w, v)
	})
	mux.HandleFunc("GET /evidence/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Store.Q.GetEvidence(r.Context(), r.PathValue("id"))
		if e != nil {
			Error(w, e)
			return
		}
		b, e := local.ReadBlob(s.Config.Root, v.BlobHash)
		if e != nil {
			Error(w, e)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})
	mux.HandleFunc("GET /jobs", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Store.Q.ListJobs(r.Context())
		if e != nil {
			Error(w, e)
			return
		}
		JSON(w, v)
	})
	mux.HandleFunc("POST /curate/{conversation}", func(w http.ResponseWriter, r *http.Request) {
		id, e := s.Worker.Enqueue(r.Context(), r.PathValue("conversation"), true)
		if e != nil {
			Error(w, e)
			return
		}
		JSON(w, map[string]string{"job_id": id})
	})
	mux.HandleFunc("POST /jobs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		n, e := s.Store.Q.RetryJob(r.Context(), r.PathValue("id"))
		if e != nil {
			Error(w, e)
			return
		}
		if n == 0 {
			Error(w, fmt.Errorf("job is running, succeeded, or missing"))
			return
		}
		JSON(w, map[string]bool{"queued": true})
	})
	mux.HandleFunc("GET /cases", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Store.Q.ListCases(r.Context())
		if e != nil {
			Error(w, e)
			return
		}
		JSON(w, v)
	})
	mux.HandleFunc("GET /cases/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Store.Q.GetCase(r.Context(), r.PathValue("id"))
		if e != nil {
			Error(w, e)
			return
		}
		h, e := s.Store.Q.CaseHistory(r.Context(), v.ID)
		if e != nil {
			Error(w, e)
			return
		}
		JSON(w, map[string]any{"case": v, "history": h})
	})
	mux.HandleFunc("POST /cases/{id}", func(w http.ResponseWriter, r *http.Request) {
		var v curator.Edit
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if e := dec.Decode(&v); e != nil {
			Error(w, e)
			return
		}
		if e := s.Worker.EditCase(r.Context(), r.PathValue("id"), v); e != nil {
			Error(w, e)
			return
		}
		JSON(w, map[string]bool{"saved": true})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject browser requests and DNS rebinding even with an accidentally exposed token.
		if r.Host != s.Config.APIAddress || r.Header.Get("Origin") != "" {
			http.Error(w, "forbidden origin/host", 403)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
		mux.ServeHTTP(w, r)
	})
}
func Run(ctx context.Context, c config.Config) error {
	lock, e := collect.Lock(c.Root, "daemon")
	if e != nil {
		return e
	}
	defer collect.Unlock(lock)
	st, e := store.Open(ctx, c)
	if e != nil {
		return e
	}
	defer st.Close()
	if e = st.Migrate(ctx); e != nil {
		return e
	}
	cl, e := collect.New(c)
	if e != nil {
		return e
	}
	token, e := c.Secret("api-token")
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &Server{Config: c, Store: st, Collector: cl, Cancel: cancel}
	s.Worker = &curator.Worker{Config: c, Store: st, Runner: curator.CodexRunner{Config: c}}
	listener, e := net.Listen("tcp", c.APIAddress)
	if e != nil {
		return e
	}
	srv := &http.Server{Handler: s.Handler(token), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	local.AtomicWrite(filepath.Join(c.Root, "state", "daemon.json"), []byte(fmt.Sprintf(`{"pid":%d,"address":%q}`, os.Getpid(), c.APIAddress)))
	defer os.Remove(filepath.Join(c.Root, "state", "daemon.json"))
	log.Printf("Imbue listening on %s; repository=%s; curator=%t", c.APIAddress, c.Repository, c.CuratorEnabled)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e := s.Worker.EnqueueReady(ctx); e != nil && ctx.Err() == nil {
					log.Printf("curator enqueue: %s", e)
				}
				if _, e := s.Worker.RunOnce(ctx); e != nil && ctx.Err() == nil {
					log.Printf("curator: %s", e)
				}
			}
		}
	}()
	defer func() { cancel(); <-workerDone }()
	tick := time.NewTicker(time.Duration(c.PollSeconds) * time.Second)
	defer tick.Stop()
	for {
		if _, e = s.Collect(ctx); e != nil && ctx.Err() == nil {
			log.Printf("collector: %s", e)
		}
		select {
		case <-ctx.Done():
			shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			return srv.Shutdown(shutdownCtx)
		case e := <-done:
			if e == http.ErrServerClosed {
				return nil
			}
			return e
		case <-tick.C:
		}
	}
}
func Request(ctx context.Context, c config.Config, method, path string, body io.Reader) ([]byte, error) {
	token, e := c.Secret("api-token")
	if e != nil {
		return nil, e
	}
	r, e := http.NewRequestWithContext(ctx, method, "http://"+c.APIAddress+path, body)
	if e != nil {
		return nil, e
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	resp, e := client.Do(r)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	if e != nil {
		return nil, e
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(b)))
	}
	return b, nil
}
