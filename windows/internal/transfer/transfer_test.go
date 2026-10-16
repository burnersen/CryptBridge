// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package transfer

import (
	"maps"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/testenv"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// sampleBundle is a Google Drive vault signed in through an own app, so that
// every kind of setting travels: the token (JSON with quotes), the app's
// codes, and the answer CryptBridge gave to rclone's question.
func sampleBundle() Bundle {
	v := vault.NewDefault("CryptBridge")
	v.Password = "pässwörd with spaces & \"quotes\""
	v.Password2 = "salt"
	return Bundle{
		CreatedAt: time.Date(2026, 10, 10, 22, 0, 0, 0, time.UTC),
		Cloud: NewCloud("drive", map[string]string{
			"type":          "drive",
			"client_id":     "123-abc.apps.googleusercontent.com",
			"client_secret": "GOCSPX-secret",
			"scope":         "drive",
			"team_drive":    "",
			"token":         `{"access_token":"T","token_type":"Bearer","refresh_token":"R","expiry":"2026-10-10T23:00:00Z"}`,
		}),
		Vault:       v,
		Fingerprint: "27CA-E698",
	}
}

func TestNewCloud(t *testing.T) {
	remote := map[string]string{"type": "dropbox", "token": "T", "batch_mode": "off"}
	before := maps.Clone(remote)
	c := NewCloud("dropbox", remote)
	want := Cloud{Service: "dropbox", Settings: map[string]string{"token": "T", "batch_mode": "off"}}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v, want %+v", c, want)
	}
	if !maps.Equal(remote, before) {
		t.Fatalf("NewCloud changed the account's settings: %v", remote)
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
	if !reflect.DeepEqual(got, want) {
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
		Cloud:       NewCloud("pcloud", map[string]string{"hostname": account.Hostname, "token": account.Token}),
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
