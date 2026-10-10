// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package vault

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

// The live tests run against a real pCloud account. They need a developer
// rclone.conf with a pcloud remote "cbpcloud" and a crypt remote "cbvault"
// (a test vault), given in CRYPTBRIDGE_DEV_RCLONE_CONF. Without it they are
// skipped. They only write into the test vault.

func liveSetup(t *testing.T) (context.Context, *rcd.Client, Config) {
	t.Helper()
	confPath := os.Getenv("CRYPTBRIDGE_DEV_RCLONE_CONF")
	if confPath == "" {
		t.Skip("CRYPTBRIDGE_DEV_RCLONE_CONF not set")
	}
	exe, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not found in PATH")
	}
	sections := readINI(t, confPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "rcd.json")
	client, err := rcd.Start(ctx, rcd.Options{RcloneExe: exe, StateFile: stateFile, ConfigFile: "NUL"})
	if err != nil {
		t.Fatalf("start rclone: %v", err)
	}
	t.Cleanup(func() { _ = rcd.Stop(context.Background(), client, stateFile) })

	pcloud := map[string]any{
		"name": "cbpcloud", "type": "pcloud",
		"parameters": map[string]string{"hostname": sections["cbpcloud"]["hostname"], "token": sections["cbpcloud"]["token"]},
		"opt":        map[string]any{"nonInteractive": true},
	}
	if err := client.Call(ctx, "config/create", pcloud, nil); err != nil {
		t.Fatalf("register pcloud: %v", err)
	}

	vaultSection := sections["cbvault"]
	cfg := NewDefault(strings.TrimPrefix(vaultSection["remote"], "cbpcloud:"))
	cfg.Password = reveal(t, exe, vaultSection["password"])
	cfg.Password2 = reveal(t, exe, vaultSection["password2"])
	return ctx, client, cfg
}

func TestLiveVerify(t *testing.T) {
	ctx, client, cfg := liveSetup(t)

	got, err := Verify(ctx, client, "cbpcloud:", cfg)
	if err != nil || got.Verdict != VerdictMatch {
		t.Fatalf("right keys: %+v, %v; want match", got, err)
	}

	wrong := cfg
	wrong.Password = "definitely-not-the-password"
	got, err = Verify(ctx, client, "cbpcloud:", wrong)
	if err != nil || got.Verdict != VerdictMismatch {
		t.Fatalf("wrong password: %+v, %v; want mismatch", got, err)
	}

	noSalt := cfg
	noSalt.Password2 = ""
	got, err = Verify(ctx, client, "cbpcloud:", noSalt)
	if err != nil || got.Verdict != VerdictMismatch {
		t.Fatalf("missing salt: %+v, %v; want mismatch", got, err)
	}

	empty := cfg
	empty.Path = cfg.Path + "-does-not-exist"
	got, err = Verify(ctx, client, "cbpcloud:", empty)
	if err != nil || got.Verdict != VerdictEmpty {
		t.Fatalf("missing folder: %+v, %v; want empty", got, err)
	}
}

func TestLiveFingerprint(t *testing.T) {
	ctx, client, cfg := liveSetup(t)
	a, err := Fingerprint(ctx, client, "cbpcloud:", cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Fingerprint(ctx, client, "cbpcloud:", cfg)
	wrong := cfg
	wrong.Password2 = "other-salt"
	c, _ := Fingerprint(ctx, client, "cbpcloud:", wrong)
	t.Logf("fingerprint %s (wrong salt: %s)", a, c)
	if a != b || a == c {
		t.Fatalf("fingerprints: same keys %s/%s, other salt %s", a, b, c)
	}
}

func TestLiveCheckFileAndDetect(t *testing.T) {
	ctx, client, cfg := liveSetup(t)
	if err := Configure(ctx, client, "cbvault", "cbpcloud:", cfg); err != nil {
		t.Fatal(err)
	}
	if err := WriteCheckFile(ctx, client, "cbvault:"); err != nil {
		t.Fatal(err)
	}
	got, err := Verify(ctx, client, "cbpcloud:", cfg)
	if err != nil || got.Verdict != VerdictMatch || !got.HasCheckFile {
		t.Fatalf("after check file: %+v, %v; want match with check file", got, err)
	}

	start := time.Now()
	candidates, err := Detect(ctx, client, "cbpcloud:")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("detect took %s, %d candidate(s): %+v", time.Since(start).Round(time.Millisecond), len(candidates), candidates)
	found := false
	for _, c := range candidates {
		if c.Path == cfg.NormalisedPath() && c.Confirmed {
			found = true
		}
	}
	if !found {
		t.Fatalf("test vault %q not detected as confirmed candidate", cfg.NormalisedPath())
	}
}

// readINI reads an rclone.conf into section -> key -> value.
func readINI(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sections := map[string]map[string]string{}
	current := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.Trim(line, "[]")
			sections[current] = map[string]string{}
			continue
		}
		if key, value, ok := strings.Cut(line, " = "); ok && current != "" {
			sections[current][key] = value
		}
	}
	return sections
}

// reveal turns an obscured rclone password back into plain text.
func reveal(t *testing.T, exe, obscured string) string {
	t.Helper()
	out, err := exec.Command(exe, "reveal", obscured).Output()
	if err != nil {
		t.Fatalf("rclone reveal: %v", err)
	}
	return strings.TrimSpace(string(out))
}
