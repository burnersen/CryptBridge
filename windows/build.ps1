# CryptBridge (https://github.com/burnersen/CryptBridge)
# Copyright (C) 2026 burnersen
# SPDX-License-Identifier: GPL-3.0-only
#
# Builds windows\build\bin\CryptBridge.exe.
#
# The version is kept in ONE place: the file VERSION in the repository root.
# This script copies it into wails.json (Windows file properties), into the
# program (shown in the window) and into linux/setup.sh, then tests and builds.
#
# Usage (from any folder):  powershell -File windows\build.ps1

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path $PSScriptRoot -Parent

# Wails CLI matching the Wails version in go.mod; "go run" uses it without
# installing anything globally, so other projects keep their own CLI.
$wailsCli = 'github.com/wailsapp/wails/v2/cmd/wails@v2.16.0'

$version = (Get-Content (Join-Path $repoRoot 'VERSION') -Raw).Trim()
if ($version -notmatch '^\d+\.\d+\.\d+$') {
    throw "VERSION must look like 1.2.3, found '$version'"
}

function Set-VersionInFile([string]$path, [string]$pattern, [string]$replacement) {
    if (-not (Test-Path $path)) { return }
    $text = [IO.File]::ReadAllText($path)
    $updated = [regex]::Replace($text, $pattern, $replacement)
    if ($updated -ne $text) {
        [IO.File]::WriteAllText($path, $updated, [Text.UTF8Encoding]::new($false))
        Write-Host "Version $version written to $path"
    }
}

Set-VersionInFile (Join-Path $PSScriptRoot 'wails.json') '"productVersion": "[^"]*"' "`"productVersion`": `"$version`""
Set-VersionInFile (Join-Path $repoRoot 'linux\setup.sh') 'readonly CRYPTBRIDGE_VERSION="[^"]*"' "readonly CRYPTBRIDGE_VERSION=`"$version`""

Push-Location $PSScriptRoot
try {
    if (-not (Test-Path 'build\appicon.png')) {
        go run ./cmd/genicon
        if ($LASTEXITCODE -ne 0) { throw 'creating the icon failed' }
    }

    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'tests failed' }

    go run $wailsCli build -trimpath -ldflags "-s -w -X main.version=$version"
    if ($LASTEXITCODE -ne 0) { throw 'wails build failed' }

    $exe = Join-Path $PSScriptRoot 'build\bin\CryptBridge.exe'
    $hash = (Get-FileHash $exe -Algorithm SHA256).Hash.ToLower()
    Write-Host ''
    Write-Host "Built $exe"
    Write-Host "Version $version, $([math]::Round((Get-Item $exe).Length / 1MB, 1)) MB, SHA-256 $hash"
}
finally {
    Pop-Location
}
