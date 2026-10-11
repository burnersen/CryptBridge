// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/lan"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
)

// TestLANFollowsDrives runs the LAN access with real drives of this PC: the
// server shows what is connected, loses a drive before the drive goes,
// survives a restart of the window, ends at once when the app locks and
// comes back after unlocking.
func TestLANFollowsDrives(t *testing.T) {
	e, ctx, letters := localEngine(t)
	root := t.TempDir()
	folderA := filepath.Join(root, "A")
	a, err := e.AddVault(ctx, newLocal("Private", letters[0], folderA, "password A", true))
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.AddVault(ctx, newLocal("Work=Docs", letters[1], filepath.Join(root, "B"), "password B", true))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ConnectAll(ctx); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, a.VaultID, StateConnected, 30*time.Second)
	waitState(t, e, b.VaultID, StateConnected, 30*time.Second)
	if err := os.WriteFile(letters[0]+`:\film.txt`, []byte("a film"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitUploadsDone(t, e, a.VaultID, time.Minute)

	port := freeTestPort(t)
	base := fmt.Sprintf("http://127.0.0.1:%d/", port)
	cfg := settings.LAN{Enabled: true, Port: port, User: "tv", Password: "short"}
	if err := e.UpdateLAN(ctx, cfg); err == nil {
		t.Fatal("a short password must be refused")
	}
	cfg.Password = "a long LAN password"
	if err := e.UpdateLAN(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if status := e.Snapshot().LAN; !status.Running || !slices.Equal(status.Folders, []string{"Private", "Work-Docs"}) {
		t.Fatalf("LAN after turning it on: %+v", status)
	}
	if got := lanListing(t, base, cfg); !slices.Equal(got, []string{"Private", "Work-Docs"}) {
		t.Fatalf("the network sees %q", got)
	}
	if status, body := lanRequest(t, "GET", base+"Private/film.txt", cfg, nil); status != http.StatusOK || body != "a film" {
		t.Fatalf("GET: %d %q", status, body)
	}
	if status, _ := lanRequest(t, "PUT", base+"Private/new.txt", cfg, strings.NewReader("x")); status < 400 {
		t.Fatalf("read only: PUT answered %d", status)
	}

	// Disconnecting takes the drive out of the network first.
	if err := e.Disconnect(ctx, b.VaultID, false); err != nil {
		t.Fatal(err)
	}
	if got := lanListing(t, base, cfg); !slices.Equal(got, []string{"Private"}) {
		t.Fatalf("after disconnecting Work=Docs the network sees %q", got)
	}

	// With changes allowed, a file from the network goes through the drive
	// into the vault.
	cfg.AllowChanges = true
	if err := e.UpdateLAN(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if status, _ := lanRequest(t, "PUT", base+"Private/from-tv.txt", cfg, strings.NewReader("from the TV")); status != http.StatusCreated {
		t.Fatalf("PUT: status %d, want 201", status)
	}
	if data, err := os.ReadFile(letters[0] + `:\from-tv.txt`); err != nil || string(data) != "from the TV" {
		t.Fatalf("on the drive: %q, %v", data, err)
	}
	waitUploadsDone(t, e, a.VaultID, time.Minute)
	if n := countFiles(t, folderA); n != 3 {
		t.Fatalf("%d file(s) in the vault folder, want check file, film and the file from the network", n)
	}

	// A restarted window takes the running server over instead of
	// interrupting the devices.
	running := serverIDs(t, e)
	e.actionMu.Lock()
	e.mu.Lock()
	e.lan = lanRuntime{}
	e.mu.Unlock()
	e.syncLANLocked(ctx, "")
	e.actionMu.Unlock()
	if after := serverIDs(t, e); len(running) != 1 || !slices.Equal(after, running) {
		t.Fatalf("servers before %q, after taking over %q; want the same one", running, after)
	}

	// Locking ends the server at once — even while the drive stays to finish
	// an upload, so rclone keeps running — and unlocking brings it back.
	if err := e.EnableLock("app password for the test"); err != nil {
		t.Fatal(err)
	}
	client := clientOf(e)
	if err := os.WriteFile(letters[0]+`:\last-minute.txt`, []byte("written just before locking"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("rclone should still run for the upload (else this part proves nothing): %v", err)
	}
	if left, err := lan.Running(ctx, client); err != nil || len(left) != 0 {
		t.Fatalf("servers after locking: %+v, %v; want none", left, err)
	}
	if e.Snapshot().LAN.Running {
		t.Fatal("snapshot: LAN still running after locking")
	}
	waitFor(t, "the lock to close the drives", time.Minute, func() bool { return clientOf(e) == nil })
	if err := e.Unlock(ctx, "app password for the test"); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, a.VaultID, StateConnected, 30*time.Second)
	if got := lanListing(t, base, cfg); !slices.Equal(got, []string{"Private"}) {
		t.Fatalf("after unlocking the network sees %q", got)
	}

	// A taken port is reported, not hidden.
	busy, err := net.Listen("tcp", fmt.Sprintf(":%d", freeTestPort(t)))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Port = busy.Addr().(*net.TCPAddr).Port
	if err := e.UpdateLAN(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if status := e.Snapshot().LAN; status.Running || !strings.Contains(status.Problem, "port") {
		t.Fatalf("LAN on a taken port: %+v", status)
	}
	busy.Close()
	if err := e.UpdateLAN(ctx, cfg); err != nil { // saving again tries at once
		t.Fatal(err)
	}
	if status := e.Snapshot().LAN; !status.Running || status.Problem != "" {
		t.Fatalf("LAN after the port became free: %+v", status)
	}

	// Turning it off stops it.
	cfg.Enabled = false
	if err := e.UpdateLAN(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if ids := serverIDs(t, e); len(ids) != 0 || e.Snapshot().LAN.Running {
		t.Fatalf("servers after turning the LAN access off: %q", ids)
	}
}

func freeTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// serverIDs lists the IDs of the LAN servers rclone runs.
func serverIDs(t *testing.T, e *Engine) []string {
	t.Helper()
	running, err := lan.Running(t.Context(), clientOf(e))
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, server := range running {
		ids = append(ids, server.ID)
	}
	return ids
}

func lanRequest(t *testing.T, method, address string, cfg settings.LAN, body io.Reader) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, address, body)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(cfg.User, cfg.Password)
	if method == "PROPFIND" {
		req.Header.Set("Depth", "1")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

var lanHref = regexp.MustCompile(`<D:href>([^<]*)</D:href>`)

// lanListing returns the folders at the root of the server.
func lanListing(t *testing.T, base string, cfg settings.LAN) []string {
	t.Helper()
	status, body := lanRequest(t, "PROPFIND", base, cfg, nil)
	if status != http.StatusMultiStatus {
		t.Fatalf("PROPFIND: status %d", status)
	}
	var names []string
	for _, match := range lanHref.FindAllStringSubmatch(body, -1) {
		name, err := url.PathUnescape(strings.Trim(match[1], "/"))
		if err != nil {
			t.Fatal(err)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
