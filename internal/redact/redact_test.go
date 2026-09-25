package redact

import (
	"strings"
	"testing"
)

func TestStructuredSecrets(t *testing.T) {
	r, e := New(nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	b, e := r.JSON([]byte(`{"access_token":"private-value","output":"password=secret sk-12345678901234567890 user@example.com","safe":"choice"}`))
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"private-value", "=secret", "12345678901234567890", "user@example.com"} {
		if strings.Contains(string(b), s) {
			t.Fatalf("leaked %s", s)
		}
	}
	if !strings.Contains(string(b), "choice") {
		t.Fatal("lost safe text")
	}
}
func TestConfiguredPattern(t *testing.T) {
	r, e := New([]string{`customer-[0-9]+`}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if r.Text("customer-123") != "[REDACTED]" {
		t.Fatal("custom rule")
	}
}
