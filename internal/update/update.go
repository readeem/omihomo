package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	Repository   = "https://github.com/readeem/omihomo"
	MaxPackage   = 128 << 20
	MaxInstaller = 32 << 20
)

//go:embed release.pub
var publicKeyPEM []byte

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

type System interface {
	Installed(context.Context) (string, error)
	Install(context.Context, string, string) error
}

type Status struct {
	Installed       string `json:"installed"`
	Available       string `json:"available"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseURL      string `json:"release_url"`
}

type Updater struct {
	client *http.Client
	system System
	key    ed25519.PublicKey
}

type release struct {
	version     string
	pkgrel      string
	packageSize int64
	packageHash string
}

func PublicKey() (ed25519.PublicKey, error) {
	block, rest := pem.Decode(publicKeyPEM)
	if block == nil || block.Type != "PUBLIC KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("invalid release public key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse release public key: %w", err)
	}
	public, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("release key is not Ed25519")
	}
	return public, nil
}

func New(client *http.Client, system System, key ed25519.PublicKey) *Updater {
	return &Updater{client: client, system: system, key: append(ed25519.PublicKey(nil), key...)}
}

func (u *Updater) Check(ctx context.Context) (Status, error) {
	r, err := u.readRelease(ctx, Repository+"/releases/latest/download")
	if err != nil {
		return Status{}, err
	}
	installed, err := u.system.Installed(ctx)
	if err != nil {
		return Status{}, err
	}
	newer := true
	if installed != "" {
		comparison, err := compare(r.version+"-"+r.pkgrel, installed)
		if err != nil {
			return Status{}, err
		}
		newer = comparison > 0
	}
	return Status{Installed: installed, Available: r.version + "-" + r.pkgrel,
		UpdateAvailable: newer, ReleaseURL: Repository + "/releases/tag/v" + r.version}, nil
}

// Apply takes a numeric release version, never a URL or path. System.Install
// must use the verified file synchronously; its private directory is removed on return.
func (u *Updater) Apply(ctx context.Context, action, version string) error {
	if (action != "install" && action != "update") || !versionPattern.MatchString(version) {
		return errors.New("expected install or update and an X.Y.Z release version")
	}
	origin := Repository + "/releases/download/v" + version
	r, err := u.readRelease(ctx, origin)
	if err != nil {
		return err
	}
	if r.version != version {
		return errors.New("signed release does not match the requested version")
	}
	validateInstalled := func() error {
		installed, err := u.system.Installed(ctx)
		if err != nil {
			return err
		}
		if installed == "" {
			if action == "update" {
				return errors.New("the package is absent; choose Install core")
			}
			return nil
		}
		if action == "install" {
			return errors.New("the package is already installed; choose Update core")
		}
		comparison, err := compare(r.version+"-"+r.pkgrel, installed)
		if err != nil {
			return err
		}
		if comparison <= 0 {
			return errors.New("refusing to reinstall or downgrade the installed package")
		}
		return nil
	}
	if err := validateInstalled(); err != nil {
		return err
	}
	// Ignore TMPDIR: privileged callers must not stage in a directory chosen
	// by the invoking account. MkdirTemp creates this directory with mode 0700.
	dir, err := os.MkdirTemp("/tmp", "omihomo-package-")
	if err != nil {
		return fmt.Errorf("create private package directory: %w", err)
	}
	defer os.RemoveAll(dir)
	name := "omihomo-bin-" + r.version + "-" + r.pkgrel + "-x86_64.pkg.tar.zst"
	path := dir + "/" + name
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create private package: %w", err)
	}
	hash := sha256.New()
	err = u.download(ctx, origin+"/"+name, io.MultiWriter(file, hash), r.packageSize)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("close package: %w", closeErr)
	}
	if hex.EncodeToString(hash.Sum(nil)) != r.packageHash {
		return errors.New("package checksum does not match the signed release")
	}
	if err := validateInstalled(); err != nil {
		return err
	}
	return u.system.Install(ctx, path, r.version+"-"+r.pkgrel)
}

func (u *Updater) readRelease(ctx context.Context, origin string) (release, error) {
	data, err := u.read(ctx, origin+"/omihomo-release.txt", 1024)
	if err != nil {
		return release{}, err
	}
	sig, err := u.read(ctx, origin+"/omihomo-release.txt.sig", ed25519.SignatureSize)
	if err != nil {
		return release{}, err
	}
	if len(u.key) != ed25519.PublicKeySize || !ed25519.Verify(u.key, data, sig) {
		return release{}, errors.New("release signature verification failed")
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) != 9 || lines[0] != "OMIHOMO-RELEASE-1" || lines[8] != "" || lines[3] != "arch=x86_64" {
		return release{}, errors.New("invalid signed release format or architecture")
	}
	values := make([]string, 7)
	for i, name := range []string{"version", "pkgrel", "arch", "package-size", "package-sha256", "installer-size", "installer-sha256"} {
		prefix := name + "="
		if !strings.HasPrefix(lines[i+1], prefix) {
			return release{}, errors.New("invalid signed release field order")
		}
		values[i] = strings.TrimPrefix(lines[i+1], prefix)
	}
	if !versionPattern.MatchString(values[0]) || !positiveNumber(values[1]) {
		return release{}, errors.New("invalid signed release version")
	}
	size, err := boundedSize(values[3], MaxPackage)
	if err != nil {
		return release{}, err
	}
	if _, err := boundedSize(values[5], MaxInstaller); err != nil {
		return release{}, err
	}
	for _, value := range []string{values[4], values[6]} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != sha256.Size || value != strings.ToLower(value) {
			return release{}, errors.New("invalid signed release checksum")
		}
	}
	return release{version: values[0], pkgrel: values[1], packageSize: size, packageHash: values[4]}, nil
}

func positiveNumber(value string) bool {
	if len(value) == 0 || len(value) > 9 || value[0] < '1' || value[0] > '9' {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func boundedSize(value string, limit int64) (int64, error) {
	if !positiveNumber(value) {
		return 0, errors.New("invalid signed release size")
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n > limit {
		return 0, errors.New("signed release exceeds the download size limit")
	}
	return n, nil
}

func compare(a, b string) (int, error) {
	parse := func(value string) ([]int, error) {
		parts := strings.Split(value, "-")
		if len(parts) != 2 || !versionPattern.MatchString(parts[0]) || !positiveNumber(parts[1]) {
			return nil, errors.New("unsupported installed package version")
		}
		var numbers []int
		for part := range strings.SplitSeq(parts[0], ".") {
			n, _ := strconv.Atoi(part)
			numbers = append(numbers, n)
		}
		n, _ := strconv.Atoi(parts[1])
		return append(numbers, n), nil
	}
	left, err := parse(a)
	if err != nil {
		return 0, err
	}
	right, err := parse(b)
	if err != nil {
		return 0, err
	}
	for i, n := range left {
		if n < right[i] {
			return -1, nil
		}
		if n > right[i] {
			return 1, nil
		}
	}
	return 0, nil
}
