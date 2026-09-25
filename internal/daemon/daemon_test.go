package daemon

import (
	"imbue/internal/config"
	"net/http/httptest"
	"testing"
)

func TestLocalAuthenticationBoundary(t *testing.T) {
	s := Server{Config: config.Config{APIAddress: "127.0.0.1:8788"}}
	for _, tt := range []struct {
		host, origin, auth string
		status             int
	}{{"127.0.0.1:8788", "", "", 401}, {"evil.test:8788", "", "Bearer secret", 403}, {"127.0.0.1:8788", "https://evil.test", "Bearer secret", 403}, {"127.0.0.1:8788", "", "Bearer secret", 404}} {
		r := httptest.NewRequest("GET", "http://"+tt.host+"/not-found", nil)
		r.Header.Set("Origin", tt.origin)
		r.Header.Set("Authorization", tt.auth)
		w := httptest.NewRecorder()
		s.Handler("secret").ServeHTTP(w, r)
		if w.Code != tt.status {
			t.Fatalf("%+v: %d", tt, w.Code)
		}
	}
}
