package migration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/metacubex/bbolt"
	"golang.org/x/sys/unix"
)

const (
	MaxSelectionCache  = 16 << 20
	MaxSelectionExport = 2 << 20
)

type Exporter func(context.Context, []byte) (map[string]string, error)

func exportSelections(ctx context.Context, root *os.Root, export Exporter) (map[string]string, error) {
	body, err := optional(root, "cache.db", MaxSelectionCache)
	if err != nil || body == nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("old selection cache is empty")
	}
	if export == nil {
		return nil, errors.New("selection cache exporter is unavailable")
	}
	selected, err := export(ctx, body)
	if err != nil {
		return nil, err
	}
	if err := ValidateSelections(selected); err != nil {
		return nil, err
	}
	return selected, nil
}

func ValidateSelections(selected map[string]string) error {
	if len(selected) > 4096 {
		return errors.New("old selections exceed import limits")
	}
	for group, member := range selected {
		if !selectionName(group) || !selectionName(member) {
			return errors.New("old selections contain invalid names")
		}
	}
	return nil
}

func selectionName(name string) bool {
	return len(name) > 0 && len(name) <= 256 && utf8.ValidString(name) && !strings.ContainsFunc(name, unicode.IsControl)
}

// ExportLegacySelections runs only in the unprivileged, resource-limited helper.
// The privileged caller receives typed JSON; it never opens the database.
func ExportLegacySelections(in io.Reader, out io.Writer) (err error) {
	if os.Geteuid() == 0 {
		return errors.New("selection export refuses root")
	}
	for resource, limit := range map[int]uint64{unix.RLIMIT_CPU: 2, unix.RLIMIT_DATA: 512 << 20, unix.RLIMIT_FSIZE: MaxSelectionCache, unix.RLIMIT_NOFILE: 64} {
		if err := unix.Setrlimit(resource, &unix.Rlimit{Cur: limit, Max: limit}); err != nil {
			return err
		}
	}
	debug.SetMemoryLimit(64 << 20)
	defer debug.SetPanicOnFault(debug.SetPanicOnFault(true))
	defer func() {
		if recover() != nil {
			err = errors.New("old selection cache is corrupt")
		}
	}()
	body, err := io.ReadAll(io.LimitReader(in, MaxSelectionCache+1))
	if err != nil {
		return err
	}
	if len(body) == 0 || len(body) > MaxSelectionCache {
		return errors.New("old selection cache exceeds import limits or is empty")
	}
	dir, err := os.MkdirTemp("", "omihomo-selection-export-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "cache.db")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return err
	}
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{ReadOnly: true, Timeout: time.Second, MaxSize: MaxSelectionCache})
	if err != nil {
		return errors.New("old selection cache is invalid")
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	selected := map[string]string{}
	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("selected"))
		if bucket == nil {
			return nil
		}
		return bucket.ForEach(func(k, v []byte) error {
			if len(selected) >= 4096 || !selectionName(string(k)) || !selectionName(string(v)) {
				return errors.New("old selections exceed import limits or contain invalid names")
			}
			selected[string(k)] = string(v)
			return nil
		})
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(selected)
}
