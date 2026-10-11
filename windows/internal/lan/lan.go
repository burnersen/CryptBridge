// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package lan shares the connected drives with the other devices of the
// home network: a WebDAV server inside the running rclone process shows
// every connected drive as one folder. The server reads and writes through
// the drive letters, so the network sees exactly what Explorer sees, and
// files written from the network wait in the drive's own upload queue.
//
// Next to the server, the package holds what belongs to it: its password,
// the addresses under which the devices reach it, and the Windows firewall
// rule that lets them in.
package lan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

const (
	// serverType is rclone's name for this kind of server.
	serverType = "webdav"

	// dirCacheTime: the server lists the drives, whose own listings are kept
	// in memory, so looking again after a few seconds costs little and shows
	// files added on this PC soon.
	dirCacheTime = "5s"

	// cacheMode: the drives have their own cache; the server needs none.
	// WebDAV uploads arrive as one stream from start to end, which works
	// without a cache (measured with rclone 1.75.1: PUT, MOVE and DELETE).
	cacheMode = "off"
)

// PortInUseError: another program listens on the port of the server.
type PortInUseError struct {
	Port int
}

func (e *PortInUseError) Error() string {
	return fmt.Sprintf("port %d is used by another program — choose another port for the LAN access", e.Port)
}

// Share is one connected drive, shown to the network as a folder.
type Share struct {
	Name string // the vault's name; becomes the folder name
	Root string // where the drive is, e.g. "Z:/"
}

// Config is how the server is reached and what it allows.
type Config struct {
	Port         int
	User         string
	Password     string
	AllowChanges bool // false: the devices can only read
}

// Plan is everything a server is made of. Servers started from equal plans
// are the same server.
type Plan struct {
	Config Config
	Shares []Share
}

// Server is a server running in rclone.
type Server struct {
	ID      string   // rclone's ID of the server
	Key     string   // identifies the plan the server was started from
	Folders []string // what the devices see, one folder per drive
	Roots   []string // the drives behind the folders
}

// Key identifies the plan without containing the password.
func (p Plan) Key() string {
	return keyOf(p.request())
}

// Folders returns the folder names the devices see.
func (p Plan) Folders() []string {
	return folderNames(p.Shares)
}

// Server describes the server that runs this plan under rclone's ID —
// one started now, or one rclone still runs from before.
func (p Plan) Server(id string) Server {
	roots := make([]string, len(p.Shares))
	for i, share := range p.Shares {
		roots[i] = share.Root
	}
	return Server{ID: id, Key: p.Key(), Folders: p.Folders(), Roots: roots}
}

// request is what rclone's serve/start gets for this plan.
func (p Plan) request() map[string]any {
	return map[string]any{
		"type":           serverType,
		"fs":             combineRemote(p.Shares, p.Folders()),
		"addr":           fmt.Sprintf(":%d", p.Config.Port),
		"user":           p.Config.User,
		"pass":           p.Config.Password,
		"read_only":      !p.Config.AllowChanges,
		"vfs_cache_mode": cacheMode,
		"dir_cache_time": dirCacheTime,
	}
}

// keyOf turns server parameters into a short fingerprint. JSON writes the
// keys of a map in sorted order, so equal parameters give equal keys.
func keyOf(params map[string]any) string {
	data, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// combineRemote builds rclone's on-the-fly "combine" remote that shows each
// drive as one folder, e.g. `:combine,upstreams="""Films=Z:/""":`.
//
// Each pair is quoted (names may contain spaces), and the whole list is
// quoted once more for the remote string; inside quotes, a quote is doubled.
func combineRemote(shares []Share, folders []string) string {
	pairs := make([]string, len(shares))
	for i, share := range shares {
		pairs[i] = quote(folders[i] + "=" + share.Root)
	}
	return ":combine,upstreams=" + quote(strings.Join(pairs, " ")) + ":"
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// fallbackFolderName names a drive whose vault name cannot be a folder name.
const fallbackFolderName = "Drive"

// folderNames turns vault names into the folder names the network sees.
// rclone splits each "folder=drive" pair at its first "=", so "=" becomes
// "-"; a name of dots only is no folder name; and a name that became equal
// to another one — devices may not tell upper and lower case apart — gets
// a number.
func folderNames(shares []Share) []string {
	names := make([]string, len(shares))
	taken := map[string]bool{}
	for i, share := range shares {
		base := strings.ReplaceAll(strings.TrimSpace(share.Name), "=", "-")
		if strings.Trim(base, ".") == "" {
			base = fallbackFolderName
		}
		name := base
		for n := 2; taken[strings.ToLower(name)]; n++ {
			name = fmt.Sprintf("%s (%d)", base, n)
		}
		taken[strings.ToLower(name)] = true
		names[i] = name
	}
	return names
}

// Start starts a server for the plan, which must share at least one drive.
//
// The port is checked first: rclone 1.75.1 does not report a taken port, it
// fails with "panic received: … nil pointer dereference" (measured; rclone
// keeps running and leaves no half-started server behind).
func Start(ctx context.Context, client *rcd.Client, p Plan) (Server, error) {
	if len(p.Shares) == 0 {
		return Server{}, errors.New("start the LAN server: no drive to share")
	}
	if !portFree(p.Config.Port) {
		return Server{}, &PortInUseError{Port: p.Config.Port}
	}
	var answer struct {
		ID string `json:"id"`
	}
	if err := client.Call(ctx, "serve/start", p.request(), &answer); err != nil {
		if !portFree(p.Config.Port) { // taken in the meantime
			return Server{}, &PortInUseError{Port: p.Config.Port}
		}
		return Server{}, fmt.Errorf("start the LAN server: %w", err)
	}
	return p.Server(answer.ID), nil
}

// portFree reports whether a server could listen on the port now.
func portFree(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

// Stop stops a server. A server that is gone already counts as stopped.
func Stop(ctx context.Context, client *rcd.Client, id string) error {
	err := client.Call(ctx, "serve/stop", map[string]any{"id": id}, nil)
	if err == nil {
		return nil
	}
	running, listErr := Running(ctx, client)
	if listErr == nil && !slices.ContainsFunc(running, func(s Server) bool { return s.ID == id }) {
		return nil
	}
	return fmt.Errorf("stop the LAN server: %w", err)
}

// StopAll stops every server of this kind in rclone.
func StopAll(ctx context.Context, client *rcd.Client) error {
	running, err := Running(ctx, client)
	if err != nil {
		return err
	}
	for _, server := range running {
		if err := Stop(ctx, client, server.ID); err != nil {
			return err
		}
	}
	return nil
}

// Running lists the servers of this kind that rclone runs. After the window
// crashed, rclone still runs the server started before; its key tells
// whether it is the one wanted now. Folders and roots are not known.
func Running(ctx context.Context, client *rcd.Client) ([]Server, error) {
	var answer struct {
		List []struct {
			ID     string         `json:"id"`
			Params map[string]any `json:"params"`
		} `json:"list"`
	}
	if err := client.Call(ctx, "serve/list", nil, &answer); err != nil {
		return nil, fmt.Errorf("list the LAN servers: %w", err)
	}
	var servers []Server
	for _, s := range answer.List {
		if s.Params["type"] == serverType {
			servers = append(servers, Server{ID: s.ID, Key: keyOf(s.Params)})
		}
	}
	return servers, nil
}
