package state

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/readeem/omihomo/internal/hosting"
)

// Hosting is a private atomic document, separate from subscriptions and editable settings.
type Hosting struct {
	Config       hosting.Config `json:"config"`
	Credential   string         `json:"credential,omitempty"`
	CredentialID string         `json:"credential_id,omitempty"`
}

// Hosting returns the last accepted desired state and repairs existing file permissions.
func (s *Store) Hosting() (Hosting, error) {
	var value Hosting
	file, err := os.OpenFile(filepath.Join(s.dir, "hosting.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return value, nil
	}
	if err != nil {
		return value, errors.New("hosting state cannot be read")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return value, errors.New("hosting state must be a regular private file")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return value, errors.New("hosting state has an invalid owner")
	}
	if err := file.Chmod(0o600); err != nil {
		return value, errors.New("hosting state cannot be made private")
	}
	if info.Size() > 64<<10 {
		return value, errors.New("hosting state exceeds its size limit")
	}
	decoder := json.NewDecoder(io.LimitReader(file, (64<<10)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Hosting{}, errors.New("hosting state is invalid")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return Hosting{}, errors.New("hosting state is invalid")
	}
	if value.Credential != "" {
		if credential, err := hosting.ValidateCredential([]byte(value.Credential)); err != nil || credential != value.Credential || value.CredentialID == "" {
			return Hosting{}, errors.New("hosting credential state is invalid")
		}
	}
	return value, nil
}

// SaveHosting atomically persists credentials and desired state, including revocations.
func (s *Store) SaveHosting(value Hosting) error { return s.write("hosting.json", value) }
