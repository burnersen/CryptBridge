// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package testenv gives live tests access to a developer's real pCloud test
// account. It reads an rclone.conf named in CRYPTBRIDGE_DEV_RCLONE_CONF with
// a pcloud remote "cbpcloud" and a crypt remote "cbvault" (a test vault).
// It is only imported by tests.
package testenv

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// Live returns rclone's path, the test account and the test vault, or skips
// the test when no developer configuration is available.
func Live(t *testing.T) (string, pcloud.Account, vault.Config) {
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

	account := pcloud.Account{Hostname: sections["cbpcloud"]["hostname"], Token: sections["cbpcloud"]["token"]}
	v := sections["cbvault"]
	cfg := vault.NewDefault(strings.TrimPrefix(v["remote"], "cbpcloud:"))
	cfg.Password = reveal(t, exe, v["password"])
	cfg.Password2 = reveal(t, exe, v["password2"])
	return exe, account, cfg
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
