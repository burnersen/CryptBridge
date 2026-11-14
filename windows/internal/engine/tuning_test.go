// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"testing"

	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/tuning"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// tuningEngine returns an engine with a cloud vault and a vault on this PC.
func tuningEngine(t *testing.T) *Engine {
	t.Helper()
	paths := testPaths(t.TempDir())
	e := New(paths, "test")
	s := settings.Defaults(paths.CacheDir)
	s.Accounts = []settings.Account{{ID: "acc", Service: "pcloud", Name: "pCloud"}}
	s.Vaults = []settings.Vault{
		{ID: "cloud", Name: "Films", Kind: settings.KindCloud, Account: "acc", Letter: "Z", Config: vault.NewDefault("Films")},
		{ID: "local", Name: "Stick", Kind: settings.KindLocal, Letter: "Y", Config: vault.NewDefault(`E:\Vault`)},
	}
	e.settings = s
	return e
}

func tunedVault(t *testing.T, e *Engine, id string) VaultSnapshot {
	t.Helper()
	for _, v := range e.Snapshot().Vaults {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("no vault %s", id)
	return VaultSnapshot{}
}

// A vault without a profile shows and uses the film values, as every drive
// did before profiles existed. A chosen profile is stored and shown.
func TestUpdateVaultProfile(t *testing.T) {
	e := tuningEngine(t)
	films := tuning.Of(tuning.Films, tuning.Values{})
	if v := tunedVault(t, e, "cloud"); v.Profile != tuning.Films || v.Tuning != films {
		t.Fatalf("fresh vault: profile %q, values %+v; want films", v.Profile, v.Tuning)
	}

	change := VaultChange{Name: "Films", Letter: "Z", Profile: tuning.Documents}
	if err := e.UpdateVault("cloud", change); err != nil {
		t.Fatal(err)
	}
	documents := tuning.Of(tuning.Documents, tuning.Values{})
	if v := tunedVault(t, e, "cloud"); v.Profile != tuning.Documents || v.Tuning != documents {
		t.Fatalf("after choosing documents: profile %q, values %+v", v.Profile, v.Tuning)
	}
	stored, _ := e.currentSettings().Vault("cloud")
	if stored.Tuning() != documents {
		t.Fatalf("the drive would connect with %+v; want the documents values", stored.Tuning())
	}

	own := tuning.Values{ChunkSize: " 4M ", ChunkSizeLimit: "64M", ReadAhead: "0", ChunkStreams: 4, WriteBack: "1m", CacheMaxAge: "7d"}
	change.Profile, change.Custom = tuning.Custom, own
	if err := e.UpdateVault("cloud", change); err != nil {
		t.Fatal(err)
	}
	own.ChunkSize = "4M" // spaces around a value are dropped
	if v := tunedVault(t, e, "cloud"); v.Profile != tuning.Custom || v.Tuning != own {
		t.Fatalf("own values: profile %q, values %+v; want %+v", v.Profile, v.Tuning, own)
	}

	// Bad values and unknown profiles change nothing.
	change.Custom.WriteBack = "never"
	if err := e.UpdateVault("cloud", change); err == nil {
		t.Fatal("a wait of \"never\" was accepted")
	}
	if err := e.UpdateVault("cloud", VaultChange{Name: "Films", Letter: "Z", Profile: "turbo"}); err == nil {
		t.Fatal("an unknown profile was accepted")
	}
	if v := tunedVault(t, e, "cloud"); v.Profile != tuning.Custom || v.Tuning != own {
		t.Fatalf("a refused change was stored: %+v", v.Tuning)
	}

	// Back to films: stored as no profile, the own values are gone.
	if err := e.UpdateVault("cloud", VaultChange{Name: "Films", Letter: "Z", Profile: tuning.Films, Custom: own}); err != nil {
		t.Fatal(err)
	}
	stored, _ = e.currentSettings().Vault("cloud")
	if stored.Profile != "" || stored.Custom != (tuning.Values{}) || stored.Tuning() != films {
		t.Fatalf("back to films: %+v", stored)
	}
}

// A vault on this PC reads from a disk: it never takes a profile.
func TestUpdateVaultProfileLocal(t *testing.T) {
	e := tuningEngine(t)
	if err := e.UpdateVault("local", VaultChange{Name: "Stick", Letter: "Y", Profile: tuning.Documents}); err != nil {
		t.Fatal(err)
	}
	stored, _ := e.currentSettings().Vault("local")
	if stored.Profile != "" {
		t.Fatalf("a local vault took the profile %q", stored.Profile)
	}
}

// The memory buffer is checked and stored; an empty value keeps it.
func TestUpdateOptionsBufferSize(t *testing.T) {
	e := tuningEngine(t)
	ctx := context.Background()
	snap := e.Snapshot()
	if snap.BufferSize != "128M" {
		t.Fatalf("default buffer %q; want 128M", snap.BufferSize)
	}
	for _, bad := range []string{"128", "2G", "lots", "64M --rc-no-auth"} {
		o := optionsOf(snap)
		o.BufferSize = bad
		if err := e.UpdateOptions(ctx, o); err == nil {
			t.Errorf("buffer %q: accepted", bad)
		}
	}
	o := optionsOf(snap)
	o.BufferSize = "32M"
	if err := e.UpdateOptions(ctx, o); err != nil {
		t.Fatal(err)
	}
	o.BufferSize = "" // an older window does not send it
	if err := e.UpdateOptions(ctx, o); err != nil {
		t.Fatal(err)
	}
	if got := e.Snapshot().BufferSize; got != "32M" {
		t.Fatalf("buffer %q; want 32M", got)
	}

	// With a drive wanted, the buffer cannot change: rclone would have to restart.
	e.update("cloud", func(rt *vaultRuntime) { rt.want = true })
	o.BufferSize = "64M"
	if err := e.UpdateOptions(ctx, o); err == nil {
		t.Fatal("the buffer changed while a drive was wanted")
	}
}
