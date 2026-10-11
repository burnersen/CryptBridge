// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

func pcloudVault(id, name, letter, path string) settings.Vault {
	return settings.Vault{ID: id, Name: name, Kind: settings.KindPCloud, Letter: letter, Config: vault.Config{Path: path}}
}

func localVault(id, name, letter, path string) settings.Vault {
	return settings.Vault{ID: id, Name: name, Kind: settings.KindLocal, Letter: letter, Config: vault.Config{Path: path}}
}

// The vault taken over from 0.3 must keep its rclone name: its cache folder,
// with files that may still wait for upload, is named after it.
func TestRemoteNames(t *testing.T) {
	migrated := pcloudVault(settings.MigratedVaultID, "Vault", "Z", "CryptBridge")
	if got := remoteOf(migrated); got != "cbvault:" {
		t.Fatalf("remote of the migrated vault = %q, want cbvault:", got)
	}
	added := localVault("", "Private", "Y", `E:\Data\Vault`)
	id := newVaultID(added, nil)
	if !strings.HasPrefix(id, "v") || len(id) != 9 {
		t.Fatalf("new ID %q; want v + 8 hex digits", id)
	}
	if again := newVaultID(localVault("", "Other name", "X", `e:\data\VAULT\`), nil); again != id {
		t.Fatalf("same place, other spelling: ID %q, want %q (the cache must be found again)", again, id)
	}
	if other := newVaultID(pcloudVault("", "", "", `E:\Data\Vault`), nil); other == id {
		t.Fatal("a pCloud vault got the ID of a local vault with the same path")
	}
	taken := []settings.Vault{{ID: id}}
	if longer := newVaultID(added, taken); longer == id || !strings.HasPrefix(longer, id) {
		t.Fatalf("ID taken: got %q, want a longer one starting with %q", longer, id)
	}
}

// The check code of a USB drive must not change with its drive letter.
func TestSplitLocalKeepsTheLetterOut(t *testing.T) {
	base, cfg := placeOf(localVault("v1", "USB", "Y", `E:\Data\Vault\`))
	if base != "E:/" || cfg.Path != "Data/Vault" {
		t.Fatalf("placeOf = %q, %q; want E:/ and Data/Vault", base, cfg.Path)
	}
	_, other := placeOf(localVault("v1", "USB", "Y", `F:\Data\Vault`))
	if other.Path != cfg.Path {
		t.Fatalf("path on F: = %q, want the same as on E: (%q)", other.Path, cfg.Path)
	}
	base, cfg = placeOf(pcloudVault("vault", "Films", "Z", "/Films/"))
	if base != "cbpcloud:" || cfg.NormalisedPath() != "Films" {
		t.Fatalf("pCloud placeOf = %q, %q", base, cfg.NormalisedPath())
	}
}

func TestCheckNameAndLetter(t *testing.T) {
	vaults := []settings.Vault{pcloudVault("a", "Films", "Z", "Films"), localVault("b", "Private", "Y", `E:\P`)}
	for _, bad := range []string{"", "   ", "films", "a/b", `a\b`, "a:b", "what?", strings.Repeat("x", 33), "tab\there"} {
		if _, err := checkName(bad, "", vaults); err == nil {
			t.Errorf("checkName(%q) accepted", bad)
		}
	}
	if got, err := checkName("  Films  ", "a", vaults); err != nil || got != "Films" {
		t.Errorf("renaming a vault to its own name: %q %v", got, err)
	}
	if got, err := checkName("Fotos 2026 (ä)", "", vaults); err != nil || got != "Fotos 2026 (ä)" {
		t.Errorf("a good name: %q %v", got, err)
	}
	if _, err := checkLetter("z:", "", vaults); err == nil {
		t.Error("a letter of another vault was accepted")
	}
	if got, err := checkLetter("z", "a", vaults); err != nil || got != "Z" {
		t.Errorf("keeping the own letter: %q %v", got, err)
	}
	if _, err := checkLetter("C", "", vaults); err == nil {
		t.Error("C: was accepted")
	}
}

func TestOverlaps(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{`E:\Vault`, `e:\vault`, true},
		{`E:\Vault\Sub`, `E:\Vault`, true},
		{`E:\Vault`, `E:\Vault\Sub`, true},
		{`E:\Vault2`, `E:\Vault`, false},
		{`E:\Vault`, `F:\Vault`, false},
		{`E:\`, `E:\Vault`, true},
	}
	for _, c := range cases {
		if got := overlaps(c.a, c.b, `\`); got != c.want {
			t.Errorf("overlaps(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if !overlaps("Films/4K", "films", "/") || overlaps("Films2", "Films", "/") {
		t.Error("pCloud paths compared wrongly")
	}
}

// Two vaults on the same files would overwrite each other's changes.
func TestCheckNoOverlap(t *testing.T) {
	vaults := []settings.Vault{pcloudVault("a", "Films", "Z", "Media/Films"), localVault("b", "Private", "Y", `E:\Private`)}
	for _, bad := range []settings.Vault{
		pcloudVault("", "", "", "media/films"),
		pcloudVault("", "", "", "Media/Films/4K"),
		pcloudVault("", "", "", "Media"),
		localVault("", "", "", `E:\Private\Inner`),
		localVault("", "", "", `e:\`),
	} {
		if err := checkNoOverlap(bad, vaults); err == nil {
			t.Errorf("%s %q accepted next to %v", bad.Kind, bad.Config.Path, vaults)
		}
	}
	for _, good := range []settings.Vault{
		pcloudVault("", "", "", "Media/Films 2"),
		localVault("", "", "", `E:\Media\Films`), // same words, other storage
		localVault("", "", "", `F:\Private`),
	} {
		if err := checkNoOverlap(good, vaults); err != nil {
			t.Errorf("%s %q refused: %v", good.Kind, good.Config.Path, err)
		}
	}
}

func TestCleanLocalFolder(t *testing.T) {
	root := t.TempDir()
	paths := testPaths(root)
	cache := filepath.Join(root, "local", "cache")
	vaults := []settings.Vault{pcloudVault("a", "Films", "Z", "Films")}
	t.Setenv("SystemRoot", `C:\Windows`)

	good := filepath.Join(root, "Vault") + `\`
	if got, err := cleanLocalFolder(good, vaults, paths, cache); err != nil || got != filepath.Join(root, "Vault") {
		t.Fatalf("a normal folder: %q %v", got, err)
	}
	for name, bad := range map[string]string{
		"empty":               "  ",
		"relative":            `Vault`,
		"network":             `\\server\share\Vault`,
		"whole drive":         `E:\`,
		"on a vault drive":    `Z:\Inside`,
		"Windows":             `C:\Windows\Vault`,
		"the cache":           filepath.Join(cache, "Vault"),
		"around the cache":    filepath.Join(root, "local"),
		"settings":            filepath.Dir(paths.SettingsFile),
		"drive-relative path": `E:Vault`,
	} {
		if got, err := cleanLocalFolder(bad, vaults, paths, cache); err == nil {
			t.Errorf("%s (%q) accepted as %q", name, bad, got)
		}
	}
}

func TestAcceptVerdict(t *testing.T) {
	cases := []struct {
		verdict            vault.Verdict
		create, unverified bool
		ok                 bool
	}{
		{vault.VerdictEmpty, true, false, true},
		{vault.VerdictMatch, true, false, false}, // never create over an existing vault
		{vault.VerdictMismatch, true, false, false},
		{vault.VerdictUnverifiable, true, true, false},
		{vault.VerdictMatch, false, false, true},
		{vault.VerdictEmpty, false, false, false},
		{vault.VerdictMismatch, false, true, false},
		{vault.VerdictUnverifiable, false, false, false},
		{vault.VerdictUnverifiable, false, true, true},
	}
	for _, c := range cases {
		if err := acceptVerdict(c.verdict, c.create, c.unverified); (err == nil) != c.ok {
			t.Errorf("acceptVerdict(%s, create=%v, acceptUnverifiable=%v) = %v; want ok=%v", c.verdict, c.create, c.unverified, err, c.ok)
		}
	}
}
