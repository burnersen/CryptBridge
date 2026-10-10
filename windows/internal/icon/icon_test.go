// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package icon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestTrayICOStructure(t *testing.T) {
	ico := TrayICO(Connected)
	var header [3]uint16
	if err := binary.Read(bytes.NewReader(ico), binary.LittleEndian, &header); err != nil {
		t.Fatal(err)
	}
	if header[0] != 0 || header[1] != 1 || int(header[2]) != len(traySizes) {
		t.Fatalf("bad ICO header %v", header)
	}
	// Every directory entry must point inside the file.
	for i := range traySizes {
		entry := ico[6+16*i : 6+16*(i+1)]
		size := binary.LittleEndian.Uint32(entry[8:])
		offset := binary.LittleEndian.Uint32(entry[12:])
		if int(offset+size) > len(ico) {
			t.Fatalf("entry %d points outside the file", i)
		}
		if entry[0] != uint8(traySizes[i]) {
			t.Fatalf("entry %d has width %d, want %d", i, entry[0], traySizes[i])
		}
	}
}

func TestPadlockShape(t *testing.T) {
	img := Padlock(64, Connected)
	inside := img.NRGBAAt(16, 48)  // body, left of the keyhole
	keyhole := img.NRGBAAt(32, 41) // centre of the keyhole: cut out
	shackle := img.NRGBAAt(32, 7)  // top of the ring
	corner := img.NRGBAAt(1, 1)    // empty
	if inside.A < 200 || shackle.A < 200 {
		t.Fatalf("body or shackle not drawn: body %v, shackle %v", inside, shackle)
	}
	if keyhole.A > 50 || corner.A != 0 {
		t.Fatalf("keyhole or corner not transparent: keyhole %v, corner %v", keyhole, corner)
	}
}

// TestWritePreview writes the icons to a folder for a visual check when
// CRYPTBRIDGE_ICON_PREVIEW is set.
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("CRYPTBRIDGE_ICON_PREVIEW")
	if dir == "" {
		t.Skip("CRYPTBRIDGE_ICON_PREVIEW not set")
	}
	app, err := AppPNG(256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(app)); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "app.png"), app, 0o644)
	states := map[string]color.NRGBA{"connected": Connected, "disconnected": Disconnected, "busy": Busy, "failed": Failed}
	for name, c := range states {
		for _, size := range []int{16, 64} {
			var buf bytes.Buffer
			if err := png.Encode(&buf, Padlock(size, c)); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%d.png", name, size)), buf.Bytes(), 0o644)
		}
	}
}
