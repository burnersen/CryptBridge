// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package passlock encrypts data with a password chosen by the user — the
// app password that locks CryptBridge. The key is derived with Argon2id, a
// method made to be slow and memory-hungry for anyone guessing passwords,
// and the data is sealed with AES-256-GCM, which also detects any change.
package passlock

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// MinPasswordLength is the shortest app password accepted. Length matters
// more than special characters; a sentence of a few words is best.
const MinPasswordLength = 8

// ErrWrongPassword is returned when the password does not open the data.
var ErrWrongPassword = errors.New("wrong password")

// Params are the Argon2id settings. They are stored with the data, so that
// stronger settings in a future version can still open older data.
type Params struct {
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memoryKiB"`
	Threads   uint8  `json:"threads"`
}

// DefaultParams follow the second recommendation of RFC 9106 (3 passes,
// 64 MiB, 4 lanes).
var DefaultParams = Params{Time: 3, MemoryKiB: 64 * 1024, Threads: 4}

const (
	saltSize = 16
	keySize  = 32 // AES-256
)

// Sealed is encrypted data with everything needed to open it again,
// except the password.
type Sealed struct {
	Params Params `json:"params"`
	Salt   []byte `json:"salt"`
	Nonce  []byte `json:"nonce"`
	Data   []byte `json:"data"`
}

// Key is a key derived from the app password. CryptBridge keeps it in memory
// while it is unlocked, so that changed settings can be sealed again without
// asking for the password each time. Wipe it when locking.
type Key struct {
	params Params
	salt   []byte
	key    []byte
}

// CheckPassword tells whether a password is acceptable as app password.
func CheckPassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return fmt.Errorf("the app password must have at least %d characters", MinPasswordLength)
	}
	return nil
}

// NewKey derives a key from a new password with a fresh random salt.
func NewKey(password string) (*Key, error) {
	if err := CheckPassword(password); err != nil {
		return nil, err
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("create salt: %w", err)
	}
	return deriveKey(password, salt, DefaultParams), nil
}

// Open derives the key from the password and opens the sealed data. The
// key is returned as well, for sealing later changes.
func Open(s Sealed, password string) ([]byte, *Key, error) {
	if len(s.Salt) == 0 || s.Params.Time == 0 || s.Params.MemoryKiB == 0 || s.Params.Threads == 0 {
		return nil, nil, errors.New("the locked settings are damaged")
	}
	key := deriveKey(password, s.Salt, s.Params)
	plain, err := key.Open(s)
	if err != nil {
		key.Wipe()
		return nil, nil, err
	}
	return plain, key, nil
}

func deriveKey(password string, salt []byte, p Params) *Key {
	return &Key{
		params: p,
		salt:   append([]byte(nil), salt...),
		key:    argon2.IDKey([]byte(password), salt, p.Time, p.MemoryKiB, p.Threads, keySize),
	}
}

// Seal encrypts data. Every call uses a new random nonce, so the same data
// never looks the same twice.
func (k *Key) Seal(plain []byte) (Sealed, error) {
	gcm, err := k.cipher()
	if err != nil {
		return Sealed{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Sealed{}, fmt.Errorf("create nonce: %w", err)
	}
	return Sealed{
		Params: k.params,
		Salt:   append([]byte(nil), k.salt...),
		Nonce:  nonce,
		Data:   gcm.Seal(nil, nonce, plain, nil),
	}, nil
}

// Open decrypts data sealed with this key. A wrong key and changed data
// look the same to AES-GCM; both give ErrWrongPassword.
func (k *Key) Open(s Sealed) ([]byte, error) {
	gcm, err := k.cipher()
	if err != nil {
		return nil, err
	}
	if len(s.Nonce) != gcm.NonceSize() {
		return nil, errors.New("the locked settings are damaged")
	}
	plain, err := gcm.Open(nil, s.Nonce, s.Data, nil)
	if err != nil {
		return nil, ErrWrongPassword
	}
	return plain, nil
}

// Wipe overwrites the key in memory. The key cannot be used afterwards.
func (k *Key) Wipe() {
	if k == nil {
		return
	}
	clear(k.key)
	k.key = nil
}

func (k *Key) cipher() (cipher.AEAD, error) {
	if len(k.key) != keySize {
		return nil, errors.New("the app password key is no longer available")
	}
	block, err := aes.NewCipher(k.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
