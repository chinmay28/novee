// Package store keeps receipts as plain files under one private directory:
//
//	ROOT/<store>/<year>/<date>_<id>.json   the record
//	ROOT/<store>/<year>/<date>_<id>.pdf    its printable copy
//	ROOT/state.json                        what the last sync of each store did
//
// Writes are atomic and Put is idempotent, so a sync can be repeated or
// interrupted at any point.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chinmay28/novee/skills/receipts/receipt"
)

// Store is a directory of receipts.
type Store struct{ root string }

// DefaultRoot is where receipts live unless NOVEE_RECEIPTS_DIR says otherwise.
func DefaultRoot() string {
	if d := os.Getenv("NOVEE_RECEIPTS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "novee", "receipts")
}

// Open returns the store rooted at root, creating the directory (mode 0700).
func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

// Root returns the directory the store lives in.
func (s *Store) Root() string { return s.root }

func (s *Store) base(r receipt.Receipt) string {
	return filepath.Join(s.root, r.Store, r.When.Format("2006"), r.When.Format("2006-01-02")+"_"+r.ID)
}

// PDFPath is where the printable copy of r is (or would be) kept.
func (s *Store) PDFPath(r receipt.Receipt) string { return s.base(r) + ".pdf" }

// HasPDF reports whether r has a printable copy.
func (s *Store) HasPDF(r receipt.Receipt) bool {
	st, err := os.Stat(s.PDFPath(r))
	return err == nil && st.Size() > 0
}

// Put saves r, replacing an earlier copy. It reports whether r is new.
func (s *Store) Put(r receipt.Receipt) (created bool, err error) {
	if err := r.Validate(); err != nil {
		return false, err
	}
	path := s.base(r) + ".json"
	_, statErr := os.Stat(path)
	created = errors.Is(statErr, fs.ErrNotExist)
	data, err := json.MarshalIndent(r, "", " ")
	if err != nil {
		return false, err
	}
	return created, writeAtomic(path, data)
}

// PutPDF saves the printable copy of r.
func (s *Store) PutPDF(r receipt.Receipt, pdf []byte) error {
	if err := r.Validate(); err != nil {
		return err
	}
	return writeAtomic(s.PDFPath(r), pdf)
}

// Filter narrows All. Zero values mean "any".
type Filter struct {
	Store    string
	From, To time.Time // inclusive, compared by calendar day
}

func (f Filter) match(r receipt.Receipt) bool {
	day := r.When.Format("2006-01-02")
	switch {
	case f.Store != "" && r.Store != f.Store:
		return false
	case !f.From.IsZero() && day < f.From.Format("2006-01-02"):
		return false
	case !f.To.IsZero() && day > f.To.Format("2006-01-02"):
		return false
	}
	return true
}

// All returns the matching receipts, oldest first. A file that cannot be read
// as a receipt is an error: silently skipping it would make "not found"
// answers wrong.
func (s *Store) All(f Filter) ([]receipt.Receipt, error) {
	var out []receipt.Receipt
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(s.root, path)
		if d.IsDir() {
			if rel == "exports" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".json") || !strings.Contains(rel, string(filepath.Separator)) {
			return nil // PDFs, and state.json at the top level
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var r receipt.Receipt
		if err := json.Unmarshal(data, &r); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		if err := r.Validate(); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		if f.match(r) {
			out = append(out, r)
		}
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].When.Equal(out[j].When) {
			return out[i].When.Before(out[j].When)
		}
		return out[i].ID < out[j].ID
	})
	return out, err
}

// SyncState is the outcome of the last sync of one store.
type SyncState struct {
	At      time.Time `json:"at"`                // when it ran
	OK      bool      `json:"ok"`                // it reached the shop and fetched
	Message string    `json:"message,omitempty"` // one line for the owner
	LastOK  time.Time `json:"last_ok,omitempty"` // last run that fetched
}

// State returns the recorded sync state of every store.
func (s *Store) State() (map[string]SyncState, error) {
	m := map[string]SyncState{}
	data, err := os.ReadFile(filepath.Join(s.root, "state.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(data, &m)
}

// SetState records the outcome of a sync and returns the previous one.
func (s *Store) SetState(name string, st SyncState) (prev SyncState, err error) {
	m, err := s.State()
	if err != nil {
		return prev, err
	}
	prev = m[name]
	if st.OK {
		st.LastOK = st.At
	} else {
		st.LastOK = prev.LastOK
	}
	m[name] = st
	data, _ := json.MarshalIndent(m, "", " ")
	return prev, writeAtomic(filepath.Join(s.root, "state.json"), data)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
