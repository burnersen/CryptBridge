// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package tuning

import (
	"strings"
	"testing"
)

// Existing vaults have no profile. They must keep exactly the values every
// drive had before profiles existed.
func TestFilmsKeepsTheOldValues(t *testing.T) {
	want := Values{ChunkSize: "64M", ChunkSizeLimit: "1G", ReadAhead: "128M", ChunkStreams: 0, WriteBack: "5s", CacheMaxAge: "1h"}
	for _, profile := range []string{"", Films, "from-a-newer-version"} {
		if got := Of(profile, Values{ChunkSize: "1M"}); got != want {
			t.Errorf("Of(%q) = %+v; want %+v", profile, got, want)
		}
	}
}

func TestEveryProfileIsValid(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Profiles() {
		if err := p.Values.Check(); err != nil {
			t.Errorf("profile %s: %v", p.ID, err)
		}
		if p.Name == "" || p.Use == "" || seen[p.ID] || !Known(p.ID) || p.ID == Custom {
			t.Errorf("profile %+v: needs a name, a use and an ID of its own", p)
		}
		seen[p.ID] = true
		if Of(p.ID, Values{}) != p.Values {
			t.Errorf("Of(%s) does not give the profile's values", p.ID)
		}
	}
	if !Known(Custom) || Known("") || Known("other") {
		t.Error("Known: only the listed profiles and Custom count")
	}
}

func TestCustomUsesTheOwnValues(t *testing.T) {
	own := Values{ChunkSize: "4M", ChunkSizeLimit: "4M", ReadAhead: "0", ChunkStreams: 8, WriteBack: "1m", CacheMaxAge: "30d"}
	if got := Of(Custom, own); got != own {
		t.Fatalf("Of(custom) = %+v; want %+v", got, own)
	}
	if err := own.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRefusesBadValues(t *testing.T) {
	good := Of(Films, Values{})
	cases := []struct {
		name   string
		change func(v *Values)
		hint   string
	}{
		{"empty chunk size", func(v *Values) { v.ChunkSize = "" }, "first read size"},
		{"chunk size without unit", func(v *Values) { v.ChunkSize = "64" }, "first read size"},
		{"chunk size too small", func(v *Values) { v.ChunkSize = "512K" }, "first read size"},
		{"chunk size too large", func(v *Values) { v.ChunkSize = "2G"; v.ChunkSizeLimit = "4G" }, "first read size"},
		{"chunk size with a flag", func(v *Values) { v.ChunkSize = "64M --rc-no-auth" }, "first read size"},
		{"limit below chunk size", func(v *Values) { v.ChunkSizeLimit = "32M" }, "largest read size"},
		{"limit off", func(v *Values) { v.ChunkSizeLimit = "off" }, "largest read size"},
		{"read ahead negative", func(v *Values) { v.ReadAhead = "-1M" }, "read ahead"},
		{"read ahead too large", func(v *Values) { v.ReadAhead = "8G" }, "read ahead"},
		{"streams negative", func(v *Values) { v.ChunkStreams = -1 }, "parallel downloads"},
		{"streams too many", func(v *Values) { v.ChunkStreams = 17 }, "parallel downloads"},
		{"streams eat memory", func(v *Values) { v.ChunkStreams = 16 }, "512M"},
		{"write back zero", func(v *Values) { v.WriteBack = "0s" }, "wait before uploading"},
		{"write back too long", func(v *Values) { v.WriteBack = "2h" }, "wait before uploading"},
		{"write back without unit", func(v *Values) { v.WriteBack = "5" }, "wait before uploading"},
		{"cache age too short", func(v *Values) { v.CacheMaxAge = "30s" }, "stay in the cache"},
		{"cache age too long", func(v *Values) { v.CacheMaxAge = "400d" }, "stay in the cache"},
	}
	for _, c := range cases {
		v := good
		c.change(&v)
		err := v.Check()
		if err == nil || !strings.Contains(err.Error(), c.hint) {
			t.Errorf("%s: Check() = %v; want an error about %q", c.name, err, c.hint)
		}
	}
}

func TestCheckBufferSize(t *testing.T) {
	for _, size := range []string{"0", "16M", "128M", "1G"} {
		if err := CheckBufferSize(size); err != nil {
			t.Errorf("CheckBufferSize(%q) = %v", size, err)
		}
	}
	for _, size := range []string{"", "128", "2G", "-1M", "128M --rc-no-auth", "off"} {
		if err := CheckBufferSize(size); err == nil {
			t.Errorf("CheckBufferSize(%q) accepted", size)
		}
	}
}
