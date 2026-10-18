package update_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/readeem/omihomo/internal/update"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type packageManager struct {
	installed     string
	beforeInstall string
	reads         int
	files         [][]byte
	paths         []string
	fail          bool
}

func (p *packageManager) Installed(context.Context) (string, error) {
	p.reads++
	if p.reads > 1 && p.beforeInstall != "" {
		return p.beforeInstall, nil
	}
	return p.installed, nil
}

func (p *packageManager) Install(_ context.Context, path, version string) error {
	file, err := os.Stat(path)
	if err != nil {
		return err
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if file.Mode().Perm() != 0600 || dir.Mode().Perm() != 0700 {
		return errors.New("download was not staged privately")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	p.files = append(p.files, data)
	p.paths = append(p.paths, path)
	if p.fail {
		return errors.New("package manager refused")
	}
	p.installed = version
	return nil
}

type fixture struct {
	key         ed25519.PrivateKey
	public      ed25519.PublicKey
	manifest    []byte
	signature   []byte
	packageData []byte
	status      int
	length      int64
	requests    int
}

func newFixture(t *testing.T, version string) *fixture {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{key: key, public: public, packageData: []byte("an invented Arch package"), status: 200, length: -1}
	f.publish(version)
	return f
}

func (f *fixture) publish(version string) {
	f.manifest = fmt.Appendf(nil, "OMIHOMO-RELEASE-1\nversion=%s\npkgrel=1\narch=x86_64\npackage-size=%d\npackage-sha256=%x\ninstaller-size=1\ninstaller-sha256=%064d\n", version, len(f.packageData), sha256.Sum256(f.packageData), 0)
	f.sign()
}

func (f *fixture) sign() { f.signature = ed25519.Sign(f.key, f.manifest) }

func (f *fixture) client() *http.Client {
	client := update.HTTPClient()
	client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
		f.requests++
		data := f.packageData
		switch {
		case strings.HasSuffix(req.URL.Path, "/omihomo-release.txt"):
			data = f.manifest
		case strings.HasSuffix(req.URL.Path, "/omihomo-release.txt.sig"):
			data = f.signature
		case !strings.HasSuffix(req.URL.Path, ".pkg.tar.zst"):
			return nil, errors.New("unexpected download")
		}
		return &http.Response{StatusCode: f.status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), ContentLength: f.length, Request: req}, nil
	})
	return client
}

func TestInstallAndUpdateVerifiedPackages(t *testing.T) {
	f := newFixture(t, "1.0.0")
	p := &packageManager{}
	u := update.New(f.client(), p, f.public)
	status, err := u.Check(t.Context())
	if err != nil || status.Installed != "" || status.Available != "1.0.0-1" {
		t.Fatalf("fresh check: %+v %v", status, err)
	}
	if err := u.Apply(t.Context(), "install", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	f.publish("1.0.1")
	status, err = u.Check(t.Context())
	if err != nil || !status.UpdateAvailable || status.Installed != "1.0.0-1" || status.Available != "1.0.1-1" || status.ReleaseURL != update.Repository+"/releases/tag/v1.0.1" {
		t.Fatalf("update check: %+v %v", status, err)
	}
	if err := u.Apply(t.Context(), "update", "1.0.1"); err != nil {
		t.Fatal(err)
	}
	if p.installed != "1.0.1-1" || len(p.files) != 2 || !bytes.Equal(p.files[1], f.packageData) {
		t.Fatalf("installed: %+v", p)
	}
	for _, path := range p.paths {
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Fatalf("private stage retained: %v", err)
		}
	}
}

func TestReleaseComparisonUsesNumericVersionAndPackageRevision(t *testing.T) {
	for _, tc := range []struct {
		available, installed string
		newer                bool
	}{
		{"1.0.10", "1.0.9-1", true}, {"1.0.1", "1.0.1-1", false}, {"1.0.1", "1.0.1-2", false}, {"1.0.0", "2.0.0-1", false},
	} {
		t.Run(tc.installed+"/"+tc.available, func(t *testing.T) {
			f := newFixture(t, tc.available)
			status, err := update.New(f.client(), &packageManager{installed: tc.installed}, f.public).Check(t.Context())
			if err != nil || status.UpdateAvailable != tc.newer {
				t.Fatalf("status %+v, error %v", status, err)
			}
		})
	}
}

func TestUntrustedReleasesNeverReachThePackageManager(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*fixture)
	}{
		{"unsigned metadata", func(f *fixture) { f.manifest[0] ^= 1 }},
		{"wrong signing key", func(f *fixture) { _, f.key, _ = ed25519.GenerateKey(rand.Reader); f.sign() }},
		{"oversized metadata", func(f *fixture) { f.manifest = bytes.Repeat([]byte("x"), 1025); f.sign() }},
		{"oversized signature", func(f *fixture) { f.signature = append(f.signature, 0) }},
		{"malformed signed data", func(f *fixture) { f.manifest = []byte("{}\n"); f.sign() }},
		{"unsupported architecture", func(f *fixture) {
			f.manifest = bytes.Replace(f.manifest, []byte("arch=x86_64"), []byte("arch=aarch64"), 1)
			f.sign()
		}},
		{"trailing fields", func(f *fixture) {
			f.manifest = append(f.manifest, []byte("url=https://example.org/evil\n")...)
			f.sign()
		}},
		{"version shell injection", func(f *fixture) {
			f.manifest = bytes.Replace(f.manifest, []byte("version=1.0.1"), []byte("version=1.0.1;id"), 1)
			f.sign()
		}},
		{"leading zero version", func(f *fixture) {
			f.manifest = bytes.Replace(f.manifest, []byte("version=1.0.1"), []byte("version=01.0.1"), 1)
			f.sign()
		}},
		{"unbounded version", func(f *fixture) {
			f.manifest = bytes.Replace(f.manifest, []byte("version=1.0.1"), []byte("version=9999999999.0.1"), 1)
			f.sign()
		}},
		{"unbounded size", func(f *fixture) {
			f.manifest = bytes.Replace(f.manifest, []byte(fmt.Sprintf("package-size=%d", len(f.packageData))), []byte("package-size=134217729"), 1)
			f.sign()
		}},
		{"zero size", func(f *fixture) {
			f.manifest = bytes.Replace(f.manifest, []byte(fmt.Sprintf("package-size=%d", len(f.packageData))), []byte("package-size=0"), 1)
			f.sign()
		}},
		{"unbounded helper", func(f *fixture) {
			f.manifest = bytes.Replace(f.manifest, []byte("installer-size=1"), []byte("installer-size=33554433"), 1)
			f.sign()
		}},
		{"invalid hash", func(f *fixture) {
			f.manifest = bytes.Replace(f.manifest, []byte("package-sha256="), []byte("package-sha256=z"), 1)
			f.sign()
		}},
		{"truncated body", func(f *fixture) { f.packageData = f.packageData[:1] }},
		{"oversized body", func(f *fixture) { f.packageData = append(f.packageData, 1) }},
		{"modified package", func(f *fixture) { f.packageData[0] ^= 1 }},
		{"wrong release", func(f *fixture) { f.publish("1.0.2") }},
		{"http failure", func(f *fixture) { f.status = 503 }},
		{"wrong content length", func(f *fixture) { f.length = 2048 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "1.0.1")
			tc.change(f)
			p := &packageManager{installed: "1.0.0-1"}
			if err := update.New(f.client(), p, f.public).Apply(t.Context(), "update", "1.0.1"); err == nil || len(p.files) != 0 {
				t.Fatalf("untrusted release installed: %v", err)
			}
		})
	}
}

func TestApplyRefusesInvalidTransitionsAndChangedInstalledVersion(t *testing.T) {
	for _, tc := range []struct{ mode, version, installed, before string }{
		{"install", "1.0.1", "1.0.0-1", ""}, {"update", "1.0.1", "", ""}, {"update", "1.0.1", "1.0.1-1", ""},
		{"update", "1.0.1", "2.0.0-1", ""}, {"update", "1.0.1", "not-a-version", ""}, {"update", "1.0.1", "1.0.0-1", "1.0.2-1"},
		{"anything", "1.0.1", "", ""}, {"install", "../1.0.1", "", ""},
	} {
		t.Run(tc.mode+"/"+tc.installed+"/"+tc.before+"/"+tc.version, func(t *testing.T) {
			f := newFixture(t, "1.0.1")
			p := &packageManager{installed: tc.installed, beforeInstall: tc.before}
			if err := update.New(f.client(), p, f.public).Apply(t.Context(), tc.mode, tc.version); err == nil || len(p.files) != 0 {
				t.Fatalf("invalid transition installed: %v", err)
			}
		})
	}
}

func TestFailedInstallCleansPrivateStage(t *testing.T) {
	f := newFixture(t, "1.0.0")
	p := &packageManager{fail: true}
	if err := update.New(f.client(), p, f.public).Apply(t.Context(), "install", "1.0.0"); err == nil {
		t.Fatal("installer failure ignored")
	}
	if len(p.paths) != 1 {
		t.Fatal("package manager not reached")
	}
	if _, err := os.Stat(filepath.Dir(p.paths[0])); !os.IsNotExist(err) {
		t.Fatal("failed stage retained")
	}
}

func TestDownloadRedirectsCannotLeaveGitHubOrLeakQueryTokens(t *testing.T) {
	for _, target := range []string{"http://github.com/file", "https://example.org/file?token=private", "https://github.com:443/file", "https://user@github.com/file", "https://127.0.0.1/file", "https://github.com.evil.example/file"} {
		t.Run(target, func(t *testing.T) {
			client := update.HTTPClient()
			calls := 0
			client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": {target}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			})
			f := newFixture(t, "1.0.0")
			_, err := update.New(client, &packageManager{}, f.public).Check(t.Context())
			if err == nil || calls != 1 || strings.Contains(err.Error(), "private") {
				t.Fatalf("redirect calls %d, error %v", calls, err)
			}
		})
	}
}

func TestCancellationStopsReleaseFetch(t *testing.T) {
	f := newFixture(t, "1.0.0")
	client := update.HTTPClient()
	client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := update.New(client, &packageManager{}, f.public).Check(ctx); err == nil {
		t.Fatal("cancelled fetch succeeded")
	}
}

func TestPackageManagerRequiresPrivilege(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires an ordinary account")
	}
	if err := (update.Pacman{}).Install(t.Context(), "/invented/package", "1.0.0-1"); err == nil {
		t.Fatal("unprivileged installation allowed")
	}
}

func TestAuthenticatedPacmanBoundary(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("OMIHOMO_ISOLATED_PACKAGE_TEST") != "1" {
		t.Skip("run tests/package_boundary_test.sh for an isolated root namespace")
	}
	for _, tc := range []struct {
		name, metadata      string
		fail, reachesPacman bool
	}{
		{"valid", "Name : omihomo-bin\nVersion : 1.0.0-1\nArchitecture : x86_64\n", false, true},
		{"wrong name", "Name : unrelated\nVersion : 1.0.0-1\nArchitecture : x86_64\n", false, false},
		{"wrong version", "Name : omihomo-bin\nVersion : 0.9.0-1\nArchitecture : x86_64\n", false, false},
		{"wrong architecture", "Name : omihomo-bin\nVersion : 1.0.0-1\nArchitecture : aarch64\n", false, false},
		{"duplicate identity", "Name : omihomo-bin\nName : unrelated\nVersion : 1.0.0-1\nArchitecture : x86_64\n", false, false},
		{"failed transaction", "Name : omihomo-bin\nVersion : 1.0.0-1\nArchitecture : x86_64\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{"package", "fail"} {
				_ = os.Remove("/tmp/omihomo-package-fixture/" + name)
			}
			if err := os.WriteFile("/tmp/omihomo-package-fixture/metadata", []byte(tc.metadata), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.fail {
				if err := os.WriteFile("/tmp/omihomo-package-fixture/fail", nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			f := newFixture(t, "1.0.0")
			err := update.New(f.client(), update.Pacman{}, f.public).Apply(t.Context(), "install", "1.0.0")
			if (err == nil) != (tc.reachesPacman && !tc.fail) {
				t.Fatalf("transaction error: %v", err)
			}
			data, readErr := os.ReadFile("/tmp/omihomo-package-fixture/package")
			if tc.reachesPacman {
				if readErr != nil || !bytes.Equal(data, f.packageData) {
					t.Fatalf("transaction bytes: %q, %v", data, readErr)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatal("wrong package identity reached the transaction")
			}
		})
	}
}
