// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package prereq

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// downloadBase is rclone's official download server.
const downloadBase = "https://downloads.rclone.org"

// downloadTimeout limits the whole download (about 25 MB).
const downloadTimeout = 10 * time.Minute

// latestVersion matches the answer of version.txt, e.g. "rclone v1.75.2".
var latestVersion = regexp.MustCompile(`^rclone v(\d+\.\d+\.\d+)\s*$`)

// Progress reports bytes done of total (total may be -1 if unknown).
type Progress func(done, total int64)

// DownloadRclone fetches the newest rclone for Windows from rclone.org,
// checks it against rclone's published SHA-256 list and puts rclone.exe
// into binDir. Nothing is kept if the checksum does not match.
func DownloadRclone(ctx context.Context, binDir string, progress Progress) (Rclone, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	client := &http.Client{}

	versionText, err := fetchText(ctx, client, downloadBase+"/version.txt")
	if err != nil {
		return Rclone{}, err
	}
	match := latestVersion.FindStringSubmatch(strings.TrimSpace(versionText))
	if match == nil {
		return Rclone{}, fmt.Errorf("unexpected answer from rclone.org: %q", versionText)
	}
	version := match[1]
	zipName := fmt.Sprintf("rclone-v%s-windows-%s.zip", version, runtime.GOARCH)

	sums, err := fetchText(ctx, client, fmt.Sprintf("%s/v%s/SHA256SUMS", downloadBase, version))
	if err != nil {
		return Rclone{}, err
	}
	want, err := checksumFor(sums, zipName)
	if err != nil {
		return Rclone{}, err
	}

	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return Rclone{}, err
	}
	zipPath := filepath.Join(binDir, zipName+".part")
	defer os.Remove(zipPath)
	got, err := fetchFile(ctx, client, fmt.Sprintf("%s/v%s/%s", downloadBase, version, zipName), zipPath, progress)
	if err != nil {
		return Rclone{}, err
	}
	if got != want {
		return Rclone{}, fmt.Errorf("the downloaded rclone does not match rclone's published checksum; it was not installed")
	}

	target := filepath.Join(binDir, "rclone.exe")
	if err := extractExe(zipPath, target); err != nil {
		return Rclone{}, err
	}
	found, err := rcloneVersion(target)
	if err != nil {
		return Rclone{}, fmt.Errorf("the downloaded rclone does not start: %w", err)
	}
	return Rclone{Path: target, Version: found}, nil
}

// checksumFor finds the SHA-256 of a file in a SHA256SUMS list. The list on
// rclone.org is PGP-signed text; the lines look like "<hash>  <file>".
func checksumFor(sums, name string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[1] == name && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("rclone's checksum list has no entry for %s", name)
}

func fetchText(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(body), err
}

// fetchFile downloads url into path and returns its SHA-256.
func fetchFile(ctx context.Context, client *http.Client, url, path string, progress Progress) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download rclone: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download rclone: %s", resp.Status)
	}

	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	reader := &countingReader{r: resp.Body, total: resp.ContentLength, progress: progress}
	if _, err := io.Copy(io.MultiWriter(file, hash), reader); err != nil {
		return "", fmt.Errorf("download rclone: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// extractExe copies rclone.exe out of the zip, via a temporary file so
// that a half-written rclone.exe never exists.
func extractExe(zipPath, target string) error {
	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open rclone zip: %w", err)
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if filepath.Base(entry.Name) != "rclone.exe" || entry.FileInfo().IsDir() {
			continue
		}
		src, err := entry.Open()
		if err != nil {
			return err
		}
		defer src.Close()
		tmp := target + ".new"
		dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			dst.Close()
			os.Remove(tmp)
			return err
		}
		if err := dst.Close(); err != nil {
			os.Remove(tmp)
			return err
		}
		return os.Rename(tmp, target)
	}
	return errors.New("rclone.exe not found in the downloaded zip")
}

// countingReader reports download progress.
type countingReader struct {
	r        io.Reader
	done     int64
	total    int64
	progress Progress
	last     time.Time
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if c.progress != nil && (time.Since(c.last) > 200*time.Millisecond || err == io.EOF) {
		c.last = time.Now()
		c.progress(c.done, c.total)
	}
	return n, err
}
