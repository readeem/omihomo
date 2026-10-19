package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSelectionExportRejectsUntrustedChildOutput(t *testing.T) {
	for _, mode := range []string{"invalid-json", "invalid-pair", "many", "stdout-limit", "stderr-limit", "failed"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSelectionExportProcess$")
			cmd.Env = append(os.Environ(), "OMIHOMO_TEST_EXPORT_MODE="+mode)
			_, err := runSelectionExport(cmd, nil)
			if err == nil {
				t.Fatal("untrusted export accepted")
			}
			if strings.Contains(err.Error(), "secret-fixture") {
				t.Fatal("export error leaked source content")
			}
		})
	}
}

func TestSelectionExportCancelsBlockedChild(t *testing.T) {
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	blockRead, blockWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer blockRead.Close()
	defer blockWrite.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSelectionExportProcess$")
	cmd.Env = append(os.Environ(), "OMIHOMO_TEST_EXPORT_MODE=blocked")
	cmd.ExtraFiles = []*os.File{readyWrite, blockRead}
	done := make(chan error, 1)
	go func() { _, err := runSelectionExport(cmd, nil); done <- err }()
	var signal [1]byte
	if _, err := readyRead.Read(signal[:]); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("blocked child survived cancellation")
	}
}

func TestSelectionExportProcess(t *testing.T) {
	switch os.Getenv("OMIHOMO_TEST_EXPORT_MODE") {
	case "":
		return
	case "invalid-json":
		fmt.Print("secret-fixture")
	case "invalid-pair":
		fmt.Print(`{"Proxy":"secret-fixture\n"}`)
	case "many":
		selected := map[string]string{}
		for i := range 4097 {
			selected[fmt.Sprint(i)] = "secret-fixture"
		}
		json.NewEncoder(os.Stdout).Encode(selected)
	case "stdout-limit":
		fmt.Print(strings.Repeat("x", (2<<20)+1))
	case "stderr-limit":
		fmt.Fprint(os.Stderr, strings.Repeat("secret-fixture", 2048))
	case "failed":
		fmt.Fprint(os.Stderr, "secret-fixture")
		os.Exit(1)
	case "blocked":
		ready := os.NewFile(3, "ready")
		block := os.NewFile(4, "block")
		ready.Write([]byte{1})
		var signal [1]byte
		block.Read(signal[:])
	}
	os.Exit(0)
}
