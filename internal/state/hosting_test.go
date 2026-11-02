package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostingStatePermissionsAndCredentialDeletion(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	const credential = "a0497f79-475f-42a8-92d7-808a49dfb36b"
	value := Hosting{Credential: credential, CredentialID: "fixture"}
	if err := store.SaveHosting(value); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hosting.json")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := store.Hosting()
	if err != nil || got.Credential != credential {
		t.Fatalf("load: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatal("existing hosting credential file remains readable by other accounts")
	}
	if err := store.SaveHosting(Hosting{}); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err = restarted.Hosting()
	if err != nil || got.Credential != "" || got.CredentialID != "" {
		t.Fatal("removed credential returned after restart")
	}
}
