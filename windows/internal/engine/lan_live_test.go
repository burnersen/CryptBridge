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
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// TestLANFollowsDrives runs both LAN accesses with real drives of this PC:
// the servers show what is connected, only the full access may write, a
// drive leaves the network before it goes, the servers survive a restart of
// the window, end at once when the app locks and come back after unlocking.
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

	// The check file stays in the vault, but the drive does not show it.
	if _, err := os.Stat(letters[0] + `:\` + vault.CheckFileName); !os.IsNotExist(err) {
		t.Fatalf("the drive shows the check file: %v", err)
	}

	streamingPort, fullPort := freeTestPort(t), freeTestPort(t)
	for fullPort == streamingPort { // Windows may hand out a port just freed again
		fullPort = freeTestPort(t)
	}
	cfg := settings.LAN{
		Streaming: settings.LANAccess{Enabled: true, Port: streamingPort, User: "tv", Password: "short"},
		Full:      settings.LANAccess{Port: fullPort, User: "phone"},
	}
	if err := e.UpdateLAN(ctx, cfg); err == nil {
		t.Fatal("a short password must be refused")
	}
	cfg.Streaming.Password = "a long LAN password"
	if err := e.UpdateLAN(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	streaming := baseOf(cfg.Streaming)
	if status := e.Snapshot().LAN; !status.Streaming.Running || !slices.Equal(status.Streaming.Folders, []string{"Private", "Work-Docs"}) ||
		status.Full.Running {
		t.Fatalf("LAN after turning the streaming access on: %+v", status)
	}
	if got := lanListing(t, streaming, cfg.Streaming); !slices.Equal(got, []string{"Private", "Work-Docs"}) {
		t.Fatalf("the network sees %q", got)
	}
	if got := lanListing(t, streaming+"Private/", cfg.Streaming); !slices.Equal(got, []string{"Private/film.txt"}) {
		t.Fatalf("in Private the network sees %q; want the film only, no check file", got)
	}
	if status, body := lanRequest(t, "GET", streaming+"Private/film.txt", cfg.Streaming, nil); status != http.StatusOK || body != "a film" {
		t.Fatalf("GET: %d %q", status, body)
	}
	if status, _ := lanRequest(t, "PUT", streaming+"Private/new.txt", cfg.Streaming, strings.NewReader("x")); status < 400 {
		t.Fatalf("streaming access: PUT answered %d", status)
	}

	// The full access needs a port and a password of its own.
	cfg.Full.Enabled = true
	cfg.Full.Password = cfg.Streaming.Password
	if err := e.UpdateLAN(ctx, cfg); err == nil || !strings.Contains(err.Error(), "password of its own") {
		t.Fatalf("the same password for both accesses: %v; want it refused", err)
	}
	cfg.Full.Password = "another long password"
	samePort := cfg
	samePort.Full.Port = cfg.Streaming.Port
	if err := e.UpdateLAN(ctx, samePort); err == nil || !strings.Contains(err.Error(), "different ports") {
		t.Fatalf("the same port for both accesses: %v; want it refused", err)
	}
	if err := e.UpdateLAN(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	full := baseOf(cfg.Full)

	// Through the full access, a file from the network goes through the drive
	// into the vault; the streaming access still refuses to write.
	if status, _ := lanRequest(t, "PUT", full+"Private/from-phone.txt", cfg.Full, strings.NewReader("from the phone")); status != http.StatusCreated {
		t.Fatalf("full access: PUT status %d, want 201", status)
	}
	if data, err := os.ReadFile(letters[0] + `:\from-phone.txt`); err != nil || string(data) != "from the phone" {
		t.Fatalf("on the drive: %q, %v", data, err)
	}
	if status, _ := lanRequest(t, "PUT", streaming+"Private/new.txt", cfg.Streaming, strings.NewReader("x")); status < 400 {
		t.Fatalf("streaming access next to the full access: PUT answered %d", status)
	}
	if status, _ := lanRequest(t, "GET", full+"Private/film.txt", cfg.Streaming, nil); status != http.StatusUnauthorized {
		t.Fatalf("the full access with the streaming password: status %d, want 401", status)
	}
	waitUploadsDone(t, e, a.VaultID, time.Minute)
	if n := countFiles(t, folderA); n != 3 {
		t.Fatalf("%d file(s) in the vault folder, want check file, film and the file from the network", n)
	}

	// Disconnecting takes the drive out of both servers first.
	if err := e.Disconnect(ctx, b.VaultID, false); err != nil {
		t.Fatal(err)
	}
	for _, access := range []settings.LANAccess{cfg.Streaming, cfg.Full} {
		if got := lanListing(t, baseOf(access), access); !slices.Equal(got, []string{"Private"}) {
			t.Fatalf("after disconnecting Work=Docs, port %d shows %q", access.Port, got)
		}
	}

	// A restarted window takes the running servers over instead of
	// interrupting the devices.
	running := serverIDs(t, e)
	e.actionMu.Lock()
	e.mu.Lock()
	e.lan = lanRuntime{}
	e.mu.Unlock()
	e.syncLANLocked(ctx, "")
	e.actionMu.Unlock()
	if after := serverIDs(t, e); len(running) != 2 || !slices.Equal(after, running) {
		t.Fatalf("servers before %q, after taking over %q; want the same two", running, after)
	}

	// Locking ends both servers at once — even while the drive stays to
	// finish an upload, so rclone keeps running — and unlocking brings them
	// back.
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
	if status := e.Snapshot().LAN; status.Streaming.Running || status.Full.Running {
		t.Fatalf("snapshot after locking: %+v; want nothing running", status)
	}
	waitFor(t, "the lock to close the drives", time.Minute, func() bool { return clientOf(e) == nil })
	if err := e.Unlock(ctx, "app password for the test"); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, a.VaultID, StateConnected, 30*time.Second)
	for _, access := range []settings.LANAccess{cfg.Streaming, cfg.Full} {
		if got := lanListing(t, baseOf(access), access); !slices.Equal(got, []string{"Private"}) {
			t.Fatalf("after unlocking, port %d shows %q", access.Port, got)
		}
	}

	// A taken port is reported for its access only; the other one keeps
	// running.
	busy, err := net.Listen("tcp", fmt.Sprintf(":%d", freeTestPort(t)))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Full.Port = busy.Addr().(*net.TCPAddr).Port
	if err := e.UpdateLAN(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if status := e.Snapshot().LAN; status.Full.Running || !strings.Contains(status.Full.Problem, "port") ||
		!status.Streaming.Running || status.Streaming.Problem != "" {
		t.Fatalf("full access on a taken port: %+v", status)
	}
	busy.Close()
	if err := e.UpdateLAN(ctx, cfg); err != nil { // saving again tries at once
		t.Fatal(err)
	}
	if status := e.Snapshot().LAN; !status.Full.Running || status.Full.Problem != "" {
		t.Fatalf("full access after the port became free: %+v", status)
	}

	// The two accesses can swap their ports in one go: the old servers stop
	// before the new ones start.
	cfg.Streaming.Port, cfg.Full.Port = cfg.Full.Port, cfg.Streaming.Port
	if err := e.UpdateLAN(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if status := e.Snapshot().LAN; !status.Streaming.Running || !status.Full.Running {
		t.Fatalf("after swapping the ports: %+v", status)
	}
	if status, _ := lanRequest(t, "PUT", baseOf(cfg.Full)+"Private/after-swap.txt", cfg.Full, strings.NewReader("x")); status != http.StatusCreated {
		t.Fatalf("full access on its new port: PUT status %d, want 201", status)
	}
	if status, _ := lanRequest(t, "PUT", baseOf(cfg.Streaming)+"Private/after-swap-2.txt", cfg.Streaming, strings.NewReader("x")); status < 400 {
		t.Fatalf("streaming access on its new port: PUT answered %d", status)
	}

	// Turning both off stops them.
	cfg.Streaming.Enabled, cfg.Full.Enabled = false, false
	if err := e.UpdateLAN(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if status := e.Snapshot().LAN; len(serverIDs(t, e)) != 0 || status.Streaming.Running || status.Full.Running {
		t.Fatalf("servers after turning the LAN access off: %q, %+v", serverIDs(t, e), status)
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

// baseOf returns the address of an access on this PC.
func baseOf(access settings.LANAccess) string {
	return fmt.Sprintf("http://127.0.0.1:%d/", access.Port)
}

// serverIDs lists the IDs of the LAN servers rclone runs, sorted.
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
	slices.Sort(ids)
	return ids
}

func lanRequest(t *testing.T, method, address string, access settings.LANAccess, body io.Reader) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, address, body)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(access.User, access.Password)
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

// lanListing returns what a folder of the server holds, as paths from the
// server's root, without the folder itself.
func lanListing(t *testing.T, address string, access settings.LANAccess) []string {
	t.Helper()
	status, body := lanRequest(t, "PROPFIND", address, access, nil)
	if status != http.StatusMultiStatus {
		t.Fatalf("PROPFIND %s: status %d", address, status)
	}
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	self := strings.Trim(parsed.Path, "/")
	var names []string
	for _, match := range lanHref.FindAllStringSubmatch(body, -1) {
		name, err := url.PathUnescape(strings.Trim(match[1], "/"))
		if err != nil {
			t.Fatal(err)
		}
		if name != self {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
