// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package rcd starts, finds and stops the background rclone process
// ("rclone rcd") and talks to it over its remote control API.
//
// The rclone process deliberately lives on its own: if the window of
// CryptBridge crashes (WebView2 does that now and then), the drive and any
// running uploads must survive. The next start of CryptBridge attaches to the
// running process again instead of starting a second one.
package rcd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// requestTimeout limits a single remote control call. Mounting and
// listing large folders can take a while; anything beyond this is a hang.
const requestTimeout = 2 * time.Minute

// Client calls the remote control API of one running rclone process.
type Client struct {
	baseURL string
	user    string
	pass    string
	pid     int
	http    *http.Client
}

// newClient builds a client for the rclone process described by state.
func newClient(state State) *Client {
	return &Client{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d/", state.Port),
		user:    state.User,
		pass:    state.Pass,
		pid:     state.PID,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// PID returns the process ID of the rclone process behind this client.
func (c *Client) PID() int {
	return c.pid
}

// APIError is an error reported by rclone itself (as opposed to a network
// problem while talking to it).
type APIError struct {
	Method  string
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("rclone %s: %s", e.Method, e.Message)
}

// Call runs one remote control method, e.g. "mount/mount". The input is sent
// as JSON; if out is not nil, the JSON answer is decoded into it.
func (c *Client) Call(ctx context.Context, method string, in, out any) error {
	if in == nil {
		in = struct{}{}
	}
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.user, c.pass)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call rclone %s: %w", method, err)
	}
	defer resp.Body.Close()

	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read rclone %s answer: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return &APIError{Method: method, Status: resp.StatusCode, Message: errorMessage(answer, resp.Status)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return fmt.Errorf("decode rclone %s answer: %w", method, err)
	}
	return nil
}

// ReadRange reads length bytes from the start of offset of a file, without
// downloading the rest. It needs rclone's object serving (--rc-serve).
// fs is a remote such as "cbpcloud:", remotePath the file inside it.
func (c *Client) ReadRange(ctx context.Context, fs, remotePath string, offset, length int64) ([]byte, error) {
	if length <= 0 {
		return nil, fmt.Errorf("ReadRange: length must be positive, got %d", length)
	}
	segments := strings.Split(strings.Trim(remotePath, "/"), "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	target := c.baseURL + "[" + fs + "]/" + strings.Join(segments, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("build read request: %w", err)
	}
	req.SetBasicAuth(c.user, c.pass)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", remotePath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read %s: %s", remotePath, resp.Status)
	}
	// A server that ignores the range answers 200 with the whole file; read
	// only what was asked for in that case.
	return io.ReadAll(io.LimitReader(resp.Body, length))
}

// errorMessage extracts rclone's own error text from a failed answer and
// falls back to the HTTP status when the answer is not the usual JSON.
func errorMessage(answer []byte, httpStatus string) string {
	var failure struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(answer, &failure) == nil && failure.Error != "" {
		return failure.Error
	}
	return httpStatus
}

// Ping checks that the rclone process is alive and answering.
func (c *Client) Ping(ctx context.Context) error {
	return c.ping(ctx)
}

// ping checks that the process answering on this port really is the rclone
// process we started. A different program may have taken over the port after
// a crash, so a mere answer is not enough — the process ID must match.
func (c *Client) ping(ctx context.Context) error {
	var answer struct {
		PID int `json:"pid"`
	}
	if err := c.Call(ctx, "core/pid", nil, &answer); err != nil {
		return err
	}
	if answer.PID != c.pid {
		return fmt.Errorf("port answered by process %d, expected rclone process %d", answer.PID, c.pid)
	}
	return nil
}

// Quit asks rclone to shut down. rclone unmounts its drives on the way out.
// The connection may break while rclone exits; that counts as success.
func (c *Client) Quit(ctx context.Context) error {
	err := c.Call(ctx, "core/quit", nil, nil)
	var apiErr *APIError
	if err != nil && errors.As(err, &apiErr) {
		return err
	}
	return nil
}
