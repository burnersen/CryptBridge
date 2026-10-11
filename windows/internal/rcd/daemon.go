// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package rcd

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	// startTimeout is how long a freshly started rclone may take until its
	// remote control API answers.
	startTimeout = 20 * time.Second

	// pollInterval is the pause between two "are you up yet?" checks.
	pollInterval = 200 * time.Millisecond

	// rcUser is the fixed user name of the remote control API. The password
	// is random per process, so a fixed user name gives nothing away.
	rcUser = "cryptbridge"

	// secretBytes is the amount of randomness in the remote control password.
	secretBytes = 24

	// startAttempts covers the rare case that the free port we picked is
	// taken by someone else before rclone binds it.
	startAttempts = 3
)

// State is what we remember about a running rclone process so that a later
// start of CryptBridge can find it again.
type State struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	User      string    `json:"user"`
	Pass      string    `json:"pass"`
	StartedAt time.Time `json:"startedAt"`
}

// Options describe how to start a new rclone process.
type Options struct {
	RcloneExe  string   // full path of rclone.exe
	StateFile  string   // where State is stored between starts
	LogFile    string   // rclone's own log file
	LogLevel   string   // rclone's --log-level; "" means INFO
	CacheDir   string   // root of rclone's cache (the VFS cache lives below)
	ConfigFile string   // rclone.conf; may be a path that does not exist
	Env        []string // extra environment, e.g. RCLONE_CONFIG_<NAME>_TYPE=...
	ExtraArgs  []string // extra global flags, e.g. --buffer-size 128M
}

// Attach connects to the rclone process recorded in stateFile. It returns
// os.ErrNotExist (wrapped) if there is nothing to attach to; a stale state
// file is removed on the way.
func Attach(ctx context.Context, stateFile string) (*Client, error) {
	state, err := readState(stateFile)
	if err != nil {
		return nil, err
	}
	if !processRunning(state.PID) {
		_ = os.Remove(stateFile)
		return nil, fmt.Errorf("rclone process %d has ended: %w", state.PID, os.ErrNotExist)
	}

	client := newClient(state)
	if err := client.ping(ctx); err != nil {
		return nil, fmt.Errorf("rclone process %d does not answer: %w", state.PID, err)
	}
	return client, nil
}

// Start launches a new hidden rclone process and waits until it answers.
func Start(ctx context.Context, opts Options) (*Client, error) {
	if opts.RcloneExe == "" || opts.StateFile == "" {
		return nil, errors.New("rcd.Start: RcloneExe and StateFile are required")
	}
	if err := os.MkdirAll(filepath.Dir(opts.StateFile), 0o700); err != nil {
		return nil, fmt.Errorf("create state folder: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= startAttempts; attempt++ {
		client, err := startOnce(ctx, opts)
		if err == nil {
			return client, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

// startOnce makes one attempt to start rclone on a fresh port.
func startOnce(ctx context.Context, opts Options) (*Client, error) {
	port, err := freeLocalPort()
	if err != nil {
		return nil, err
	}
	pass, err := randomSecret()
	if err != nil {
		return nil, err
	}

	args := []string{
		"rcd",
		"--rc-addr", fmt.Sprintf("127.0.0.1:%d", port),
		"--rc-user", rcUser,
		"--rc-pass", pass,
		// Lets CryptBridge read the first bytes of a file (vault detection)
		// through the same password-protected local API.
		"--rc-serve",
	}
	if opts.ConfigFile != "" {
		args = append(args, "--config", opts.ConfigFile)
	}
	if opts.CacheDir != "" {
		args = append(args, "--cache-dir", opts.CacheDir)
	}
	if opts.LogFile != "" {
		level := opts.LogLevel
		if level == "" {
			level = "INFO"
		}
		args = append(args, "--log-file", opts.LogFile, "--log-level", level)
	}
	args = append(args, opts.ExtraArgs...)

	cmd := exec.Command(opts.RcloneExe, args...)
	cmd.Env = append(os.Environ(), opts.Env...)
	cmd.SysProcAttr = hiddenDetachedProcess()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start rclone: %w", err)
	}

	// Wait() in the background only tells us whether rclone died early.
	// It does not tie rclone's life to ours: when CryptBridge exits, rclone
	// keeps running, which is exactly the point.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	state := State{PID: cmd.Process.Pid, Port: port, User: rcUser, Pass: pass, StartedAt: time.Now()}
	client := newClient(state)
	if err := waitUntilReady(ctx, client, exited); err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	if err := writeState(opts.StateFile, state); err != nil {
		_ = client.Quit(ctx)
		return nil, err
	}
	return client, nil
}

// waitUntilReady polls the new process until its API answers, it dies, or
// the time is up.
func waitUntilReady(ctx context.Context, client *Client, exited <-chan error) error {
	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			return fmt.Errorf("rclone stopped right after starting (see its log file): %v", err)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
		if client.ping(ctx) == nil {
			return nil
		}
	}
	return fmt.Errorf("rclone did not answer within %s", startTimeout)
}

// stopTimeout is how long rclone may take to unmount and exit after being
// asked to quit.
const stopTimeout = 15 * time.Second

// Stop shuts the process down through its API, waits until it has really
// ended and forgets it.
func Stop(ctx context.Context, client *Client, stateFile string) error {
	if err := client.Quit(ctx); err != nil {
		return err
	}
	deadline := time.Now().Add(stopTimeout)
	for processRunning(client.PID()) {
		if time.Now().After(deadline) {
			return fmt.Errorf("rclone process %d is still running %s after being asked to quit", client.PID(), stopTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	_ = os.Remove(stateFile)
	return nil
}

// readState loads the state file.
func readState(path string) (State, error) {
	var state State
	data, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("read %s: %w", path, err)
	}
	if state.PID <= 0 || state.Port <= 0 {
		return state, fmt.Errorf("read %s: incomplete state: %w", path, os.ErrNotExist)
	}
	return state, nil
}

// writeState stores the state file atomically, so that a crash in the middle
// never leaves half a file behind.
func writeState(path string, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

// freeLocalPort asks the system for a free TCP port on the loopback address.
func freeLocalPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find a free port: %w", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// randomSecret returns a URL-safe random password.
func randomSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("create random password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
