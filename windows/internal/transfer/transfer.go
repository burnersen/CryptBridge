// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package transfer packs the sign-in to a cloud account and the vault keys
// into an encrypted text block that the Linux setup can read. Nobody has to type
// passwords twice, and a typing error can no longer split the vault.
//
// The block is exactly what this command produces, so the Linux side needs
// nothing but the standard openssl tool to open it:
//
//	openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -md sha256 -salt -a
//
// It is protected by a one-time transfer code shown in the window.
package transfer

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"strings"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

const (
	// Format identifies the content; the Linux setup checks it. Format 1
	// (up to CryptBridge 0.7) carried a pCloud sign-in only; the Linux setup
	// still reads it.
	Format = "cryptbridge-transfer-2"

	// Iterations of PBKDF2-SHA256 (OWASP recommendation 2023). The Linux
	// setup passes the same number to openssl.
	Iterations = 600_000

	BeginMarker = "-----BEGIN CRYPTBRIDGE TRANSFER-----"
	EndMarker   = "-----END CRYPTBRIDGE TRANSFER-----"

	// codeAlphabet: upper case and digits without 0/O and 1/I, so the code
	// can be read from the screen and typed without mistakes (5 bits each).
	codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	codeGroups   = 5
	codeGroupLen = 5 // 25 characters = 125 bits

	opensslMagic = "Salted__"
	saltLen      = 8
	keyLen       = 32 // AES-256
	lineLen      = 64 // openssl -a writes and expects 64 characters per line
)

// Bundle is what travels to the server.
type Bundle struct {
	Format      string       `json:"format"`
	CreatedAt   time.Time    `json:"createdAt"`
	Cloud       Cloud        `json:"cloud"`
	Vault       vault.Config `json:"vault"`
	Fingerprint string       `json:"fingerprint"`
}

// Cloud is the sign-in to the cloud account of the vault: the service and
// rclone's settings of the account, as rclone keeps them. The Linux setup
// writes them into its rclone configuration unchanged, so the server signs
// in just like the Windows program (token, own app, OneDrive's drive ...).
type Cloud struct {
	Service  string            `json:"service"`  // rclone's backend name, e.g. "drive"
	Settings map[string]string `json:"settings"` // without "type": that is Service
}

// NewCloud returns the sign-in to an account of service from rclone's
// settings of the account. It works on a copy: the account's own settings
// stay as they are.
func NewCloud(service string, remote map[string]string) Cloud {
	settings := maps.Clone(remote)
	delete(settings, "type")
	return Cloud{Service: service, Settings: settings}
}

// NewCode returns a random transfer code like "K7Q2M-…-…-…-…".
func NewCode() (string, error) {
	limit := big.NewInt(int64(len(codeAlphabet)))
	groups := make([]string, codeGroups)
	for g := range groups {
		buf := make([]byte, codeGroupLen)
		for i := range buf {
			n, err := rand.Int(rand.Reader, limit)
			if err != nil {
				return "", fmt.Errorf("create transfer code: %w", err)
			}
			buf[i] = codeAlphabet[n.Int64()]
		}
		groups[g] = string(buf)
	}
	return strings.Join(groups, "-"), nil
}

// NormaliseCode keeps only the characters a code is made of, upper case, so
// that "k7q2m-abcde" typed by hand works too, and so does a code with
// invisible characters picked up by copying. The Linux setup does the same.
func NormaliseCode(code string) string {
	return strings.Map(func(r rune) rune {
		if 'a' <= r && r <= 'z' {
			r -= 'a' - 'A'
		}
		if strings.ContainsRune(codeAlphabet, r) {
			return r
		}
		return -1
	}, code)
}

// Seal encrypts the bundle with the code and returns the text block.
func Seal(b Bundle, code string) (string, error) {
	password := NormaliseCode(code)
	if len(password) < codeGroups*codeGroupLen {
		return "", errors.New("transfer code too short")
	}
	b.Format = Format
	plain, err := json.Marshal(b)
	if err != nil {
		return "", err
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, iv, err := deriveKey(password, salt)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad(plain, aes.BlockSize)
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)

	raw := append(append([]byte(opensslMagic), salt...), encrypted...)
	encoded := base64.StdEncoding.EncodeToString(raw)
	var out strings.Builder
	out.WriteString(BeginMarker + "\n")
	for len(encoded) > lineLen {
		out.WriteString(encoded[:lineLen] + "\n")
		encoded = encoded[lineLen:]
	}
	out.WriteString(encoded + "\n" + EndMarker + "\n")
	return out.String(), nil
}

// Open reverses Seal. A wrong code is reported as such.
func Open(text, code string) (Bundle, error) {
	var b Bundle
	body, err := between(text, BeginMarker, EndMarker)
	if err != nil {
		return b, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(body), ""))
	if err != nil {
		return b, fmt.Errorf("the transfer block is damaged: %w", err)
	}
	if len(raw) < len(opensslMagic)+saltLen+aes.BlockSize || string(raw[:len(opensslMagic)]) != opensslMagic {
		return b, errors.New("the transfer block is damaged")
	}
	salt := raw[len(opensslMagic) : len(opensslMagic)+saltLen]
	encrypted := raw[len(opensslMagic)+saltLen:]
	if len(encrypted)%aes.BlockSize != 0 {
		return b, errors.New("the transfer block is damaged")
	}

	key, iv, err := deriveKey(NormaliseCode(code), salt)
	if err != nil {
		return b, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return b, err
	}
	plain := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, encrypted)
	plain, ok := pkcs7Unpad(plain, aes.BlockSize)
	errWrongCode := errors.New("wrong transfer code (or the block is damaged)")
	if !ok {
		return b, errWrongCode
	}
	if err := json.Unmarshal(plain, &b); err != nil || b.Format != Format {
		return b, errWrongCode
	}
	return b, nil
}

// deriveKey is openssl's "-pbkdf2 -md sha256": one PBKDF2 output split into
// the AES key and the CBC start vector.
func deriveKey(password string, salt []byte) (key, iv []byte, err error) {
	material, err := pbkdf2.Key(sha256.New, password, salt, Iterations, keyLen+aes.BlockSize)
	if err != nil {
		return nil, nil, err
	}
	return material[:keyLen], material[keyLen:], nil
}

func pkcs7Pad(data []byte, size int) []byte {
	n := size - len(data)%size
	return append(data, bytes.Repeat([]byte{byte(n)}, n)...)
}

func pkcs7Unpad(data []byte, size int) ([]byte, bool) {
	if len(data) == 0 || len(data)%size != 0 {
		return nil, false
	}
	n := int(data[len(data)-1])
	if n == 0 || n > size || n > len(data) {
		return nil, false
	}
	for _, c := range data[len(data)-n:] {
		if int(c) != n {
			return nil, false
		}
	}
	return data[:len(data)-n], true
}

// between returns the text between the two markers.
func between(text, begin, end string) (string, error) {
	start := strings.Index(text, begin)
	stop := strings.Index(text, end)
	if start < 0 || stop < start {
		return "", errors.New("this is not a CryptBridge transfer block")
	}
	return text[start+len(begin) : stop], nil
}
