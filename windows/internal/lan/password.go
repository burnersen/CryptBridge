// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package lan

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// MinPort and MaxPort: ports below 1024 are left to system services.
	MinPort = 1024
	MaxPort = 65535

	// MinPasswordLength: the server answers every guess at once, so a
	// password chosen by the user must be long.
	MinPasswordLength = 12
	maxPasswordLength = 128

	// passwordAlphabet: lower-case letters and digits without the ones that
	// are easily confused (l/1, o/0) — the password is often typed with the
	// remote control of a TV.
	passwordAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"

	// A new password has 4 groups of 4 characters: 80 bits of randomness.
	passwordGroups      = 4
	passwordGroupLength = 4
)

// userPattern: user names are entered on TVs and phones; plain characters
// avoid surprises there. A colon would break the sign-in (user:password).
var userPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// NewPassword returns a random password that is easy to type, such as
// "k7qm-4xpa-r2tn-w9hc".
func NewPassword() (string, error) {
	limit := big.NewInt(int64(len(passwordAlphabet)))
	groups := make([]string, passwordGroups)
	for g := range groups {
		group := make([]byte, passwordGroupLength)
		for i := range group {
			n, err := rand.Int(rand.Reader, limit)
			if err != nil {
				return "", fmt.Errorf("create random password: %w", err)
			}
			group[i] = passwordAlphabet[n.Int64()]
		}
		groups[g] = string(group)
	}
	return strings.Join(groups, "-"), nil
}

// CheckPassword makes sure a password is long enough and can be typed.
func CheckPassword(password string) error {
	length := utf8.RuneCountInString(password)
	switch {
	case length < MinPasswordLength:
		return fmt.Errorf("the LAN password needs at least %d characters", MinPasswordLength)
	case length > maxPasswordLength:
		return fmt.Errorf("the LAN password may have at most %d characters", maxPasswordLength)
	case strings.TrimSpace(password) != password:
		return errors.New("the LAN password cannot start or end with a space")
	case strings.ContainsFunc(password, func(r rune) bool { return r < ' ' }):
		return errors.New("the LAN password cannot contain control characters")
	}
	return nil
}

// CheckUser makes sure a user name works on every device.
func CheckUser(user string) error {
	if !userPattern.MatchString(user) {
		return errors.New("the LAN user name needs 1 to 32 letters, digits, dots, dashes or underscores")
	}
	return nil
}

// CheckPort makes sure the port can be used.
func CheckPort(port int) error {
	if port < MinPort || port > MaxPort {
		return fmt.Errorf("the LAN port must be between %d and %d", MinPort, MaxPort)
	}
	return nil
}
