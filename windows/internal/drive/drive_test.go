// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package drive

import "testing"

func TestNormaliseLetter(t *testing.T) {
	good := map[string]string{"z": "Z", "Z:": "Z", `Z:\`: "Z", " d ": "D", "x:": "X"}
	for in, want := range good {
		got, err := normaliseLetter(in)
		if err != nil || got != want {
			t.Errorf("normaliseLetter(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "A", "b:", "C", "ZZ", "1", "Ä", `\\`} {
		if got, err := normaliseLetter(in); err == nil {
			t.Errorf("normaliseLetter(%q) = %q, want an error", in, got)
		}
	}
}

func TestPendingUploads(t *testing.T) {
	s := Status{UploadsInProgress: 2, UploadsQueued: 3}
	if got := s.PendingUploads(); got != 5 {
		t.Fatalf("PendingUploads = %d, want 5", got)
	}
}

func TestLetterInUseSystemDrive(t *testing.T) {
	// C: exists on every Windows machine this program runs on.
	inUse, err := letterInUse("C")
	if err != nil || !inUse {
		t.Fatalf("letterInUse(C) = %v, %v; want true", inUse, err)
	}
}
