package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestInitObservesAllFutureCodexWorkspaces(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(t.TempDir(), "imbue-home")
	c, e := Init(root, repo)
	if e != nil {
		t.Fatal(e)
	}
	if !c.ObserveAll || c.ObserveAllSince == "" {
		t.Fatalf("all-workspace scope was not enabled: %+v", c)
	}
	if _, e = time.Parse(time.RFC3339Nano, c.ObserveAllSince); e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(root)
	if e != nil || !loaded.ObserveAll || loaded.ObserveAllSince != c.ObserveAllSince {
		t.Fatalf("scope did not persist: %+v %v", loaded, e)
	}
}
