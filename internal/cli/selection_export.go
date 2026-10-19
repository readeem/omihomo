package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
	"time"

	"github.com/readeem/omihomo/internal/migration"
)

func exportOwner() (int, int, error) {
	uid, gid := os.Getuid(), os.Getgid()
	if os.Geteuid() != 0 {
		return uid, gid, nil
	}
	uid, err := invokingUID(os.Getenv)
	if err != nil {
		return 0, 0, err
	}
	account, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return 0, 0, err
	}
	gid, err = strconv.Atoi(account.Gid)
	return uid, gid, err
}

func selectionExporter(uid, gid int) migration.Exporter {
	return func(ctx context.Context, body []byte) (map[string]string, error) {
		executable, err := os.Executable()
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "--internal-export-selections")
		temp, err := os.MkdirTemp("/tmp", "omihomo-selection-helper-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(temp)
		if os.Geteuid() == 0 {
			if uid <= 0 || gid < 0 {
				return nil, errors.New("selection export needs a nonroot owner")
			}
			if err := os.Chown(temp, uid, gid); err != nil {
				return nil, err
			}
		}
		cmd.Env = []string{"PATH=/usr/bin", "GOMEMLIMIT=64MiB", "GOMAXPROCS=1", "TMPDIR=" + temp}
		if os.Geteuid() == 0 {
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}}
		}
		return runSelectionExport(cmd, body)
	}
}

func runSelectionExport(cmd *exec.Cmd, body []byte) (map[string]string, error) {
	cmd.Stdin = bytes.NewReader(body)
	cmd.WaitDelay = time.Second
	var output bytes.Buffer
	cmd.Stdout = &exportOutput{out: &output, left: migration.MaxSelectionExport}
	cmd.Stderr = &exportOutput{out: io.Discard, left: 16 << 10}
	if err := cmd.Run(); err != nil {
		return nil, errors.New("old selection cache export failed; cache may be corrupt or exceed limits")
	}
	var selected map[string]string
	if err := json.Unmarshal(output.Bytes(), &selected); err != nil {
		return nil, errors.New("old selection cache export is invalid")
	}
	if err := migration.ValidateSelections(selected); err != nil {
		return nil, err
	}
	return selected, nil
}

type exportOutput struct {
	out  io.Writer
	left int
}

func (w *exportOutput) Write(p []byte) (int, error) {
	if len(p) > w.left {
		return 0, errors.New("selection export exceeds output limit")
	}
	n, err := w.out.Write(p)
	w.left -= n
	return n, err
}
