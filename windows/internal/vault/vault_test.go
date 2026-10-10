// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package vault

import "testing"

func TestLooksEncrypted(t *testing.T) {
	encrypted := []string{
		"kml48mer0vm8jmp6iobco9cvvn74vu5us5a1vdu9kagqc222t6fg", // measured: 52 chars, 2 blocks
		"pnesujkb6vsfqmo00u12f2gbig",                           // measured: 26 chars, 1 block
		"mk5rut3bh6b0fad4eka77pevfk",
	}
	for _, name := range encrypted {
		if !LooksEncrypted(name) {
			t.Errorf("LooksEncrypted(%q) = false, want true", name)
		}
	}
	plain := []string{
		"", "Movies", "desktop.ini", "Avatar (2009).mkv",
		"kml48mer0vm8jmp6iobco9cvvn74vu5us5a1vdu9kagqc222t6f", // 51 chars: impossible length
		"pnesujkb6vsfqmo00u12f2gbiw",                          // 'w' is outside base32hex; 26 is no base64 length
		"PNESUJKB6VSFQMO00U12F2GBIG",                          // upper case is not rclone's base32
	}
	for _, name := range plain {
		if LooksEncrypted(name) {
			t.Errorf("LooksEncrypted(%q) = true, want false", name)
		}
	}
}

func TestValidLength(t *testing.T) {
	// base32: ceil(128*n/5) -> 26, 52, 77, 103
	for _, n := range []int{26, 52, 77, 103} {
		if !validLength(n, 5) {
			t.Errorf("base32 length %d should be valid", n)
		}
	}
	// base64: ceil(128*n/6) -> 22, 43, 64, 86
	for _, n := range []int{22, 43, 64, 86} {
		if !validLength(n, 6) {
			t.Errorf("base64 length %d should be valid", n)
		}
	}
	for _, n := range []int{0, 1, 25, 27, 51, 53} {
		if validLength(n, 5) {
			t.Errorf("base32 length %d should be invalid", n)
		}
	}
}

func TestFormatFingerprint(t *testing.T) {
	a := formatFingerprint("CryptBridge-Test/vault", "pnesujkb6vsfqmo00u12f2gbig/mk5rut3bh6b0fad4eka77pevfk")
	if len(a) != 9 || a[4] != '-' {
		t.Fatalf("fingerprint %q does not have the form XXXX-XXXX", a)
	}
	if b := formatFingerprint("CryptBridge-Test/vault", "pnesujkb6vsfqmo00u12f2gbig/mk5rut3bh6b0fad4eka77pevfk"); a != b {
		t.Fatalf("same input gave %q and %q", a, b)
	}
	if c := formatFingerprint("Other", "pnesujkb6vsfqmo00u12f2gbig/mk5rut3bh6b0fad4eka77pevfk"); a == c {
		t.Fatal("a different folder must give a different fingerprint")
	}
}

func TestNormalisedPathAndValidate(t *testing.T) {
	c := NewDefault(` /Videos\Vault/ `)
	if got := c.NormalisedPath(); got != "Videos/Vault" {
		t.Fatalf("NormalisedPath = %q", got)
	}
	if err := c.Validate(); err == nil {
		t.Fatal("empty password must be rejected")
	}
	c.Password = "secret"
	if err := c.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	c.Path = "/"
	if err := c.Validate(); err == nil {
		t.Fatal("top level of pCloud must be rejected as vault folder")
	}
}

func TestParametersSalt(t *testing.T) {
	c := NewDefault("V")
	c.Password = "p"
	if _, ok := c.parameters("cbpcloud:")["password2"]; ok {
		t.Fatal("an empty salt must not be passed to rclone")
	}
	c.Password2 = "s"
	if got := c.parameters("cbpcloud:")["remote"]; got != "cbpcloud:V" {
		t.Fatalf("remote = %q", got)
	}
}
