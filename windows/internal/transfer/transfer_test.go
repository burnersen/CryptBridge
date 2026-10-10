// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package transfer

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/testenv"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

func sampleBundle() Bundle {
	v := vault.NewDefault("CryptBridge")
	v.Password = "pässwörd with spaces & \"quotes\""
	v.Password2 = "salt"
	return Bundle{
		CreatedAt:   time.Date(2026, 10, 10, 22, 0, 0, 0, time.UTC),
		PCloud:      pcloud.Account{Hostname: pcloud.HostEU, Token: `{"access_token":"T","token_type":"bearer"}`},
		Vault:       v,
		Fingerprint: "27CA-E698",
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	code, err := NewCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 29 || strings.Count(code, "-") != 4 {
		t.Fatalf("code %q does not look like XXXXX-XXXXX-XXXXX-XXXXX-XXXXX", code)
	}
	text, err := Seal(sampleBundle(), code)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if len(line) > lineLen && !strings.HasPrefix(line, "-----") {
			t.Fatalf("line longer than %d characters: %q", lineLen, line)
		}
	}
	// Typed by hand: lower case, without dashes, with spaces around.
	got, err := Open("some text before\r\n"+text, "  "+strings.ToLower(strings.ReplaceAll(code, "-", ""))+" ")
	if err != nil {
		t.Fatal(err)
	}
	want := sampleBundle()
	want.Format = Format
	if got != want {
		t.Fatalf("round trip changed the bundle:\n got %+v\nwant %+v", got, want)
	}
}

func TestOpenWrongCode(t *testing.T) {
	code, _ := NewCode()
	text, _ := Seal(sampleBundle(), code)
	other, _ := NewCode()
	if _, err := Open(text, other); err == nil || !strings.Contains(err.Error(), "wrong transfer code") {
		t.Fatalf("wrong code: got %v", err)
	}
}

func TestOpenDamaged(t *testing.T) {
	code, _ := NewCode()
	if _, err := Open("hello", code); err == nil {
		t.Fatal("text without markers accepted")
	}
	if _, err := Open(BeginMarker+"\n!!!\n"+EndMarker, code); err == nil {
		t.Fatal("broken base64 accepted")
	}
}

func TestSealRejectsShortCode(t *testing.T) {
	if _, err := Seal(sampleBundle(), "ABC"); err == nil {
		t.Fatal("short code accepted")
	}
}

// TestWriteLiveTransfer writes a real transfer block (developer's pCloud
// sign-in plus a test vault given in the environment) for testing the Linux
// setup end to end. Only runs when CRYPTBRIDGE_LIVE_TRANSFER_OUT is set.
func TestWriteLiveTransfer(t *testing.T) {
	out := os.Getenv("CRYPTBRIDGE_LIVE_TRANSFER_OUT")
	if out == "" {
		t.Skip("CRYPTBRIDGE_LIVE_TRANSFER_OUT not set")
	}
	_, account, v := testenv.Live(t)
	if p := os.Getenv("CRYPTBRIDGE_LIVE_VAULT_PATH"); p != "" {
		v = vault.NewDefault(p)
		v.Password = os.Getenv("CRYPTBRIDGE_LIVE_VAULT_PASSWORD")
		v.Password2 = os.Getenv("CRYPTBRIDGE_LIVE_VAULT_SALT")
	}
	code, _ := NewCode()
	text, err := Seal(Bundle{
		CreatedAt:   time.Now().UTC(),
		PCloud:      account,
		Vault:       v,
		Fingerprint: os.Getenv("CRYPTBRIDGE_LIVE_FINGERPRINT"),
	}, code)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out+`\live.cbtransfer`, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out+`\live.code`, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestWriteSampleForOpenSSL writes a block and its code for a check with
// the real openssl on Linux, when CRYPTBRIDGE_TRANSFER_SAMPLE is set.
func TestWriteSampleForOpenSSL(t *testing.T) {
	dir := os.Getenv("CRYPTBRIDGE_TRANSFER_SAMPLE")
	if dir == "" {
		t.Skip("CRYPTBRIDGE_TRANSFER_SAMPLE not set")
	}
	code, _ := NewCode()
	text, err := Seal(sampleBundle(), code)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+`\sample.cbtransfer`, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+`\sample.code`, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
}
