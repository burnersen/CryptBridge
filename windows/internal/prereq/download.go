// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package prereq

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// downloadTimeout limits the download from one server (about 30 MB).
const downloadTimeout = 10 * time.Minute

// releaseVersion matches a released CryptBridge version, e.g. "0.9.2";
// builds without the build script are called "dev".
var releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// Progress reports bytes done of total (total may be -1 if unknown).
type Progress func(done, total int64)

// DownloadRclone fetches rclone RcloneVersion for this PC, checks it against
// the checksum built into CryptBridge and puts rclone.exe into binDir, in
// place of an older one. cryptBridgeVersion names the CryptBridge release
// that keeps a copy of rclone's ZIP files as the last source. Nothing is
// kept if no server delivers a file with the right checksum.
func DownloadRclone(ctx context.Context, binDir, cryptBridgeVersion string, progress Progress) (Rclone, error) {
	zipName, want, err := rcloneZip(runtime.GOARCH)
	if err != nil {
		return Rclone{}, err
	}
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return Rclone{}, err
	}
	zipPath := filepath.Join(binDir, zipName+".part")
	defer os.Remove(zipPath)
	if err := fetchVerified(ctx, rcloneSources(zipName, cryptBridgeVersion), zipPath, want, progress); err != nil {
		return Rclone{}, err
	}

	target := filepath.Join(binDir, "rclone.exe")
	if err := replaceExe(zipPath, target); err != nil {
		return Rclone{}, err
	}
	found, err := ReadRcloneVersion(target)
	if err != nil {
		return Rclone{}, fmt.Errorf("the downloaded rclone does not start: %w", err)
	}
	if found != RcloneVersion {
		return Rclone{}, fmt.Errorf("the downloaded rclone reports version %s instead of %s", found, RcloneVersion)
	}
	return Rclone{Path: target, Version: found}, nil
}

// rcloneZip returns the name and the SHA-256 of rclone's ZIP file for a
// processor type (runtime.GOARCH).
func rcloneZip(goarch string) (name, checksum string, err error) {
	platform := "windows-" + goarch
	checksum, ok := rcloneZipSHA256[platform]
	if !ok {
		return "", "", fmt.Errorf("CryptBridge has no rclone for this kind of processor (%s); it knows amd64 and arm64", goarch)
	}
	return fmt.Sprintf("rclone-v%s-%s.zip", RcloneVersion, platform), checksum, nil
}

// rcloneSources lists where the ZIP file is downloaded from, in this order:
// rclone's download server, rclone's release on GitHub and the unchanged
// copy kept with CryptBridge's own release (only a released version has one).
func rcloneSources(zipName, cryptBridgeVersion string) []string {
	urls := []string{
		fmt.Sprintf("https://downloads.rclone.org/v%s/%s", RcloneVersion, zipName),
		fmt.Sprintf("https://github.com/rclone/rclone/releases/download/v%s/%s", RcloneVersion, zipName),
	}
	if releaseVersion.MatchString(cryptBridgeVersion) {
		urls = append(urls, fmt.Sprintf("https://github.com/burnersen/CryptBridge/releases/download/v%s/%s",
			cryptBridgeVersion, zipName))
	}
	return urls
}

// fetchVerified downloads url after url into path until one file has the
// SHA-256 want. A file that does not match is deleted before the next try.
func fetchVerified(ctx context.Context, urls []string, path, want string, progress Progress) error {
	client := &http.Client{}
	var problems []string
	for _, url := range urls {
		got, err := fetchFile(ctx, client, url, path, progress)
		if err == nil && got == want {
			return nil
		}
		if err == nil {
			err = errors.New("the file does not match the checksum built into CryptBridge")
		}
		_ = os.Remove(path)
		problems = append(problems, fmt.Sprintf("%s: %v", url, err))
		if ctx.Err() != nil {
			break // cancelled: the other servers would fail the same way
		}
	}
	return fmt.Errorf("the download of rclone %s failed, nothing was installed (%s)", RcloneVersion, strings.Join(problems, "; "))
}

// fetchFile downloads url into path and returns its SHA-256.
func fetchFile(ctx context.Context, client *http.Client, url, path string, progress Progress) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New(resp.Status)
	}

	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	reader := &countingReader{r: resp.Body, total: resp.ContentLength, progress: progress}
	if _, err := io.Copy(io.MultiWriter(file, hash), reader); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// replaceExe puts the rclone.exe from the zip at target, which keeps its
// path: the firewall rule of the LAN access names it. Windows refuses to
// overwrite a running program but lets it be renamed (measured), so the
// new file is written next to it as .new, the old one becomes .old, and the
// new one takes its place. An old rclone that still runs keeps its .old
// file; CleanUp removes it at a later start.
func replaceExe(zipPath, target string) error {
	newPath, oldPath := target+".new", target+".old"
	if err := extractExe(zipPath, newPath); err != nil {
		return err
	}
	_ = os.Remove(oldPath) // left from an earlier replacement; if still in use, the rename below says so
	if err := os.Rename(target, oldPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = os.Remove(newPath)
		return fmt.Errorf("the old rclone could not be set aside, so it was left as it is (%v). "+
			"Sign out of Windows or restart the PC, then try again", err)
	}
	if err := os.Rename(newPath, target); err != nil {
		_ = os.Rename(oldPath, target) // never leave the folder without an rclone.exe
		_ = os.Remove(newPath)
		return fmt.Errorf("the new rclone could not be put in place: %w", err)
	}
	_ = os.Remove(oldPath) // fails while the old rclone still runs
	return nil
}

// extractExe copies rclone.exe out of the zip into path, which is only a
// temporary name: replaceExe renames it, so a half-written rclone.exe never
// exists.
func extractExe(zipPath, path string) error {
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
		dst, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			dst.Close()
			os.Remove(path)
			return err
		}
		if err := dst.Close(); err != nil {
			os.Remove(path)
			return err
		}
		return nil
	}
	return errors.New("rclone.exe not found in the downloaded zip")
}

// CleanUp removes what an earlier download or replacement left in binDir:
// rclone.exe.old and unfinished *.new and *.part files. A file still in use
// (an old rclone that keeps running) simply stays until the next time.
func CleanUp(binDir string) {
	leftovers := []string{filepath.Join(binDir, "rclone.exe.old")}
	for _, pattern := range []string{"*.new", "*.part"} {
		matches, _ := filepath.Glob(filepath.Join(binDir, pattern))
		leftovers = append(leftovers, matches...)
	}
	for _, path := range leftovers {
		_ = os.Remove(path)
	}
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
