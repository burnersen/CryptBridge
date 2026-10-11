// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/appdir"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// TestSeedLocalProfile prepares a test profile for trying the real program
// with a local vault: it creates the vault in <profile>\vault and the
// settings in <profile>\roaming and <profile>\local, which the program uses
// when started with APPDATA and LOCALAPPDATA pointing there:
//
//	CRYPTBRIDGE_SEED_LOCAL_PROFILE=C:\temp\cbprofile CRYPTBRIDGE_SEED_LETTER=Q go test ./internal/engine -run SeedLocal
func TestSeedLocalProfile(t *testing.T) {
	profile := os.Getenv("CRYPTBRIDGE_SEED_LOCAL_PROFILE")
	if profile == "" {
		t.Skip("CRYPTBRIDGE_SEED_LOCAL_PROFILE not set")
	}
	t.Setenv("APPDATA", filepath.Join(profile, "roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(profile, "local"))
	paths, err := appdir.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e := New(paths, "seed")
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Shutdown(context.Background(), true) }()

	cfg := vault.NewDefault(filepath.Join(profile, "vault"))
	cfg.Password = "test password of the seeded vault"
	check, err := e.AddVault(ctx, NewVault{Kind: settings.KindLocal, Name: "Test vault", Letter: os.Getenv("CRYPTBRIDGE_SEED_LETTER"),
		AutoConnect: true, Config: cfg, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("vault %s with check code %s in %s", check.VaultID, check.Fingerprint, cfg.Path)
}
