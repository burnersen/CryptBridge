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

// Each drive must show only its own uploads (measured: rclone reports one
// list for all drives, each transfer with the remote it goes to).
func TestTransfersOf(t *testing.T) {
	all := []remoteTransfer{
		{Transfer: Transfer{Name: "film.mkv"}, SrcFs: `:local{R5KD5}://?/C:/cache/vfs/cbvault`, DstFs: "cbvault:"},
		{Transfer: Transfer{Name: "photo.jpg"}, SrcFs: `:local{R5KD5}://?/C:/cache/vfs/cbv1a2b3c4d`, DstFs: "cbv1a2b3c4d:"},
		{Transfer: Transfer{Name: "download.bin"}, SrcFs: "cbvault:", DstFs: ""},
	}
	got := transfersOf(all, "cbvault:")
	if len(got) != 2 || got[0].Name != "film.mkv" || got[1].Name != "download.bin" {
		t.Fatalf("transfers of cbvault: %+v", got)
	}
	if got := transfersOf(all, "cbother:"); got == nil || len(got) != 0 {
		t.Fatalf("transfers of a drive without any: %#v; want an empty list", got)
	}
}

// A local vault must not get the streaming values meant for pCloud, and a
// pCloud vault must keep them.
func TestVfsOptions(t *testing.T) {
	local := Settings{Local: true, CacheMaxSize: "20G", CacheMinFreeSpace: "10G"}.vfsOptions()
	if local["CacheMode"] != LocalCacheMode || local["CacheMaxAge"] != LocalCacheMaxAge || local["ChunkSize"] != nil {
		t.Fatalf("local options: %v", local)
	}
	cloud := Settings{CacheMaxSize: "20G", CacheMinFreeSpace: "10G"}.vfsOptions()
	if cloud["CacheMode"] != CacheMode || cloud["ChunkSize"] != ChunkSize || cloud["DirCacheTime"] != DirCacheTime || cloud["CacheMaxAge"] != nil {
		t.Fatalf("pCloud options: %v", cloud)
	}
}

// Hidden names apply to the top folder only and are taken literally, even
// if they contain characters that rclone's filters read as patterns.
func TestHideFilter(t *testing.T) {
	got := Settings{Hide: []string{".cryptbridge-vault", "a*b?[c]{d}"}}.hideFilter()["ExcludeRule"]
	want := []string{"/.cryptbridge-vault", `/a\*b\?\[c\]\{d\}`}
	rules, ok := got.([]string)
	if !ok || len(rules) != len(want) || rules[0] != want[0] || rules[1] != want[1] {
		t.Fatalf("ExcludeRule = %#v; want %#v", got, want)
	}
}

func TestLetterInUseSystemDrive(t *testing.T) {
	// C: exists on every Windows machine this program runs on.
	inUse, err := letterInUse("C")
	if err != nil || !inUse {
		t.Fatalf("letterInUse(C) = %v, %v; want true", inUse, err)
	}
}
