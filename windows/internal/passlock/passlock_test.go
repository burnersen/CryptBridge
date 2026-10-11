// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package passlock

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

const testPassword = "correct horse battery staple"

func TestSealOpenRoundTrip(t *testing.T) {
	key, err := NewKey(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte(`{"token":"VERY-SECRET"}`)
	sealed, err := key.Seal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed.Data, []byte("VERY-SECRET")) {
		t.Fatal("sealed data contains the secret in readable form")
	}

	start := time.Now()
	plain, reopened, err := Open(sealed, testPassword)
	t.Logf("deriving the key took %s", time.Since(start))
	if err != nil || !bytes.Equal(plain, secret) {
		t.Fatalf("got %q, %v", plain, err)
	}

	// The key from Open seals changes that the password opens again.
	again, err := reopened.Seal([]byte("changed"))
	if err != nil {
		t.Fatal(err)
	}
	if plain, _, err := Open(again, testPassword); err != nil || string(plain) != "changed" {
		t.Fatalf("resealed: got %q, %v", plain, err)
	}
}

func TestWrongPasswordAndTampering(t *testing.T) {
	key, _ := NewKey(testPassword)
	sealed, _ := key.Seal([]byte("secret"))

	if _, _, err := Open(sealed, testPassword+"x"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password: got %v", err)
	}
	tampered := sealed
	tampered.Data = append([]byte(nil), sealed.Data...)
	tampered.Data[0] ^= 1
	if _, _, err := Open(tampered, testPassword); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("changed data: got %v", err)
	}
	broken := sealed
	broken.Salt = nil
	if _, _, err := Open(broken, testPassword); err == nil || errors.Is(err, ErrWrongPassword) {
		t.Fatalf("missing salt: got %v, want a damage error", err)
	}
}

func TestSameDataSealsDifferently(t *testing.T) {
	key, _ := NewKey(testPassword)
	a, _ := key.Seal([]byte("same"))
	b, _ := key.Seal([]byte("same"))
	if bytes.Equal(a.Nonce, b.Nonce) || bytes.Equal(a.Data, b.Data) {
		t.Fatal("two seals of the same data must differ")
	}
}

func TestWipedKeyIsUnusable(t *testing.T) {
	key, _ := NewKey(testPassword)
	key.Wipe()
	if _, err := key.Seal([]byte("x")); err == nil {
		t.Fatal("a wiped key must not seal")
	}
	var none *Key
	none.Wipe() // must not panic
}

func TestCheckPassword(t *testing.T) {
	if err := CheckPassword("1234567"); err == nil {
		t.Error("7 characters accepted")
	}
	if err := CheckPassword("äöüßäöüß"); err != nil {
		t.Errorf("8 non-ASCII characters rejected: %v", err)
	}
	if _, err := NewKey("short"); err == nil {
		t.Error("NewKey accepted a short password")
	}
}
