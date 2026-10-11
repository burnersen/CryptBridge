// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package lan

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

func TestFolderNames(t *testing.T) {
	shares := []Share{
		{Name: "Films"},
		{Name: "Ä Docs, 'n' #1 %20"}, // kept as it is (measured: rclone serves it)
		{Name: "A=B"},                // rclone would split at "="
		{Name: "A-B"},                // now the same as the one before
		{Name: "films"},              // differs from "Films" only in case
		{Name: ".."},
		{Name: "..."},
	}
	want := []string{"Films", "Ä Docs, 'n' #1 %20", "A-B", "A-B (2)", "films (2)", "Drive", "Drive (2)"}
	if got := folderNames(shares); !slices.Equal(got, want) {
		t.Fatalf("folder names\n got %q\nwant %q", got, want)
	}
}

func TestCombineRemote(t *testing.T) {
	shares := []Share{{Name: "Films", Root: "Z:/"}, {Name: "My Docs", Root: "Y:/"}}
	got := combineRemote(shares, folderNames(shares))
	want := `:combine,upstreams="""Films=Z:/"" ""My Docs=Y:/""":`
	if got != want {
		t.Fatalf("combine remote\n got %s\nwant %s", got, want)
	}
}

func TestPlanKey(t *testing.T) {
	plan := Plan{Config: Config{Port: 8080, User: "tv", Password: "secret-password-1"}, Shares: []Share{{Name: "Films", Root: "Z:/"}}}
	same := plan
	same.Shares = slices.Clone(plan.Shares)
	if plan.Key() != same.Key() {
		t.Fatal("equal plans must have equal keys")
	}
	for name, change := range map[string]func(p *Plan){
		"password":      func(p *Plan) { p.Config.Password = "secret-password-2" },
		"allow changes": func(p *Plan) { p.Config.AllowChanges = true },
		"port":          func(p *Plan) { p.Config.Port = 8081 },
		"drive":         func(p *Plan) { p.Shares = append(p.Shares, Share{Name: "Docs", Root: "Y:/"}) },
	} {
		changed := plan
		changed.Shares = slices.Clone(plan.Shares)
		change(&changed)
		if changed.Key() == plan.Key() {
			t.Errorf("changing the %s must change the key", name)
		}
	}
	if strings.Contains(plan.Key(), "secret") {
		t.Fatal("the key must not contain the password")
	}
}

func TestNewPassword(t *testing.T) {
	pattern := regexp.MustCompile(`^[` + passwordAlphabet + `]{4}(-[` + passwordAlphabet + `]{4}){3}$`)
	seen := map[string]bool{}
	for range 50 {
		password, err := NewPassword()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(password) {
			t.Fatalf("password %q does not look like xxxx-xxxx-xxxx-xxxx", password)
		}
		if err := CheckPassword(password); err != nil {
			t.Fatalf("a new password must be accepted: %v", err)
		}
		if seen[password] {
			t.Fatalf("password %q came twice", password)
		}
		seen[password] = true
	}
}

func TestChecks(t *testing.T) {
	for _, password := range []string{"short", " leading-space-x", "trailing-space ", "with\ttab-inside", strings.Repeat("x", 129)} {
		if CheckPassword(password) == nil {
			t.Errorf("password %q must be refused", password)
		}
	}
	for _, password := range []string{"twelve chars", "Ein ganzer Satz mit Umlauten: äöü!"} {
		if err := CheckPassword(password); err != nil {
			t.Errorf("password %q: %v", password, err)
		}
	}
	for _, user := range []string{"", "with space", "colon:user", "äöü", strings.Repeat("u", 33)} {
		if CheckUser(user) == nil {
			t.Errorf("user %q must be refused", user)
		}
	}
	for _, user := range []string{"cryptbridge", "tv.living-room_1"} {
		if err := CheckUser(user); err != nil {
			t.Errorf("user %q: %v", user, err)
		}
	}
	for _, port := range []int{0, 80, 1023, 65536} {
		if CheckPort(port) == nil {
			t.Errorf("port %d must be refused", port)
		}
	}
	for _, port := range []int{1024, 8080, 65535} {
		if err := CheckPort(port); err != nil {
			t.Errorf("port %d: %v", port, err)
		}
	}
}

func TestAddresses(t *testing.T) {
	for _, address := range Addresses(8080) {
		u, err := url.Parse(address)
		if err != nil || u.Scheme != "http" || u.Port() != "8080" || u.Path != "/" {
			t.Fatalf("address %q is not like http://192.168.1.20:8080/", address)
		}
		if ip := net.ParseIP(u.Hostname()); ip == nil || !homeAddress(ip) {
			t.Fatalf("address %q is not a home network address", address)
		}
	}
}

func TestEncodeScript(t *testing.T) {
	script := "Write-Output 'Grüße'"
	raw, err := base64.StdEncoding.DecodeString(encodeScript(script))
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if got := string(utf16.Decode(units)); got != script {
		t.Fatalf("decoded %q, want %q", got, script)
	}
	if got := psQuote(`C:\Users\O'Brien\rclone.exe`); got != `'C:\Users\O''Brien\rclone.exe'` {
		t.Fatalf("psQuote: %s", got)
	}
}

// The layout of SHELLEXECUTEINFOW must match Windows exactly, or the
// administrator prompt would get garbage.
func TestShellExecuteInfoSize(t *testing.T) {
	if size := unsafe.Sizeof(shellExecuteInfo{}); size != 112 {
		t.Fatalf("size %d, want 112 (SHELLEXECUTEINFOW on 64-bit Windows)", size)
	}
}

// ReadFirewall only looks; it changes nothing. With the rclone of this PC,
// the log shows the rules Windows has about it.
func TestReadFirewall(t *testing.T) {
	program := `C:\no-such-folder\rclone.exe`
	if found, err := exec.LookPath("rclone"); err == nil {
		if real, err := filepath.EvalSymlinks(found); err == nil {
			program = real
		}
	}
	state, err := ReadFirewall(context.Background(), 8080, program)
	if err != nil {
		t.Fatal(err)
	}
	if state.Blocking == nil || state.Others == nil || state.Networks == nil {
		t.Fatalf("lists must not be nil: %+v", state)
	}
	for _, rule := range state.Others {
		if rule.ID == "" {
			t.Fatalf("rule %+v has no ID, so it could not be removed", rule)
		}
	}
	t.Logf("firewall for %s: %+v", program, state)
}

func TestRemoveRulesScript(t *testing.T) {
	script := removeRulesScript([]string{`TCP Query User{1}C:\x\rclone.exe`, `it's'; Remove-Item C:\ #`})
	want := `Remove-NetFirewallRule -Name 'TCP Query User{1}C:\x\rclone.exe' -ErrorAction SilentlyContinue` + "\n" +
		`Remove-NetFirewallRule -Name 'it''s''; Remove-Item C:\ #' -ErrorAction SilentlyContinue`
	if script != want {
		t.Fatalf("script\n%s\nwant\n%s", script, want)
	}
}

// ---------- with a real rclone (from PATH) on folders of this PC ----------

func startRclone(t *testing.T) (context.Context, *rcd.Client) {
	t.Helper()
	exe, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not found in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	stateFile := filepath.Join(t.TempDir(), "rcd.json")
	client, err := rcd.Start(ctx, rcd.Options{RcloneExe: exe, StateFile: stateFile, ConfigFile: "NUL"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rcd.Stop(context.Background(), client, stateFile) })
	return ctx, client
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// folderWith creates a folder holding one file.
func folderWith(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// webdav sends one request to the server and returns status and body.
func webdav(t *testing.T, method, address, user, password string, body io.Reader) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, address, body)
	if err != nil {
		t.Fatal(err)
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}
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

var hrefPattern = regexp.MustCompile(`<D:href>([^<]*)</D:href>`)

// listing returns the names in a folder of the server.
func listing(t *testing.T, address, user, password string) []string {
	t.Helper()
	status, body := webdav(t, "PROPFIND", address, user, password, nil)
	if status != http.StatusMultiStatus {
		t.Fatalf("PROPFIND %s: status %d", address, status)
	}
	base, _ := url.Parse(address)
	var names []string
	for _, match := range hrefPattern.FindAllStringSubmatch(body, -1) {
		path, err := url.PathUnescape(match[1])
		if err != nil {
			t.Fatal(err)
		}
		if name := strings.Trim(strings.TrimPrefix(path, base.Path), "/"); name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func TestServerShowsTheShares(t *testing.T) {
	ctx, client := startRclone(t)
	films := folderWith(t, "film.txt", "a film")
	docs := folderWith(t, "letter.txt", "a letter")
	plan := Plan{
		Config: Config{Port: freePort(t), User: "tv", Password: "secret-password-1"},
		Shares: []Share{
			{Name: "Films", Root: filepath.ToSlash(films)},
			{Name: "Ä Docs, 'n' #1", Root: filepath.ToSlash(docs)},
		},
	}
	server, err := Start(ctx, client, plan)
	if err != nil {
		t.Fatal(err)
	}
	if server.Key != plan.Key() || !slices.Equal(server.Folders, plan.Folders()) {
		t.Fatalf("server %+v does not match its plan", server)
	}
	root := "http://127.0.0.1:" + url.PathEscape(strings.TrimPrefix(plan.request()["addr"].(string), ":")) + "/"

	if status, _ := webdav(t, "PROPFIND", root, "", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("without password: status %d, want 401", status)
	}
	if status, _ := webdav(t, "PROPFIND", root, "tv", "wrong-password", nil); status != http.StatusUnauthorized {
		t.Fatalf("wrong password: status %d, want 401", status)
	}
	if got, want := listing(t, root, "tv", "secret-password-1"), []string{"Films", "Ä Docs, 'n' #1"}; !slices.Equal(got, want) {
		t.Fatalf("root folders %q, want %q", got, want)
	}
	docsURL := root + url.PathEscape("Ä Docs, 'n' #1") + "/"
	if status, body := webdav(t, "GET", docsURL+"letter.txt", "tv", "secret-password-1", nil); status != http.StatusOK || body != "a letter" {
		t.Fatalf("GET: status %d, body %q", status, body)
	}
	if status, _ := webdav(t, "PUT", docsURL+"new.txt", "tv", "secret-password-1", strings.NewReader("x")); status < 400 {
		t.Fatalf("read only: PUT answered %d", status)
	}
	if _, err := os.Stat(filepath.Join(docs, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read only: the file was written anyway")
	}

	// rclone still runs the server: a restarted window finds it by its key.
	running, err := Running(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 || running[0].ID != server.ID || running[0].Key != plan.Key() {
		t.Fatalf("running servers %+v; want the one started", running)
	}

	// A second server on the same port fails with a clear message.
	var inUse *PortInUseError
	if _, err := Start(ctx, client, plan); !errors.As(err, &inUse) || inUse.Port != plan.Config.Port {
		t.Fatalf("second server on the same port: %v, want PortInUseError", err)
	}

	if err := Stop(ctx, client, server.ID); err != nil {
		t.Fatal(err)
	}
	if err := Stop(ctx, client, server.ID); err != nil {
		t.Fatalf("stopping a stopped server: %v", err)
	}
	if running, err := Running(ctx, client); err != nil || len(running) != 0 {
		t.Fatalf("after stop: %+v, %v", running, err)
	}

	// With changes allowed, a file written from the network lands in the folder.
	plan.Config.AllowChanges = true
	if _, err := Start(ctx, client, plan); err != nil {
		t.Fatal(err)
	}
	if status, _ := webdav(t, "PUT", docsURL+"new.txt", "tv", "secret-password-1", strings.NewReader("from the TV")); status != http.StatusCreated {
		t.Fatalf("PUT: status %d, want 201", status)
	}
	if data, err := os.ReadFile(filepath.Join(docs, "new.txt")); err != nil || string(data) != "from the TV" {
		t.Fatalf("written file: %q, %v", data, err)
	}
	if err := StopAll(ctx, client); err != nil {
		t.Fatal(err)
	}
	if running, err := Running(ctx, client); err != nil || len(running) != 0 {
		t.Fatalf("after StopAll: %+v, %v", running, err)
	}
}
