package mihomo

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/metacubex/bbolt"

	"github.com/readeem/omihomo/internal/core"
)

// ImportSelections seeds an unused core home without starting the engine. The
// caller must own home exclusively; an existing cache is never replaced.
func (*Core) ImportSelections(home string, p core.Profile, selections map[string]string) error {
	if len(selections) == 0 {
		return nil
	}
	if len(selections) > 4096 || len(core.FilterSelections(p, selections)) != len(selections) {
		return errors.New("invalid imported selections")
	}
	if err := privateDir(home); err != nil {
		return err
	}
	path := filepath.Join(home, "cache.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		os.Remove(path)
		return err
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucket([]byte("selected"))
		if err != nil {
			return err
		}
		for group, member := range selections {
			if err := bucket.Put([]byte(group), []byte(member)); err != nil {
				return err
			}
		}
		return nil
	})
	err = errors.Join(err, db.Close())
	if err != nil {
		os.Remove(path)
	}
	return err
}
