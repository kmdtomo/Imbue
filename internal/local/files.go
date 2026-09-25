package local

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// AtomicWrite makes file contents durable before publishing the name.
func AtomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func PutBlob(root string, data []byte) (string, error) {
	h := Hash(data)
	p := filepath.Join(root, "data", "blobs", h[:2], h+".json")
	if old, err := os.ReadFile(p); err == nil {
		if Hash(old) != h {
			return "", fmt.Errorf("blob integrity error: %s", h)
		}
		return h, nil
	}
	return h, AtomicWrite(p, data)
}
func ReadBlob(root, h string) ([]byte, error) {
	if len(h) != 64 {
		return nil, fmt.Errorf("invalid blob hash")
	}
	if _, e := hex.DecodeString(h); e != nil {
		return nil, e
	}
	b, e := os.ReadFile(filepath.Join(root, "data", "blobs", h[:2], h+".json"))
	if e != nil {
		return nil, e
	}
	if Hash(b) != h {
		return nil, fmt.Errorf("blob integrity error: %s", h)
	}
	return b, nil
}
