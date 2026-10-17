# CryptBridge (https://github.com/burnersen/CryptBridge)
# Copyright (C) 2026 burnersen
# SPDX-License-Identifier: GPL-3.0-only
#
# Collects what a GitHub release carries next to CryptBridge.exe, in
# windows\build\bin\release\: rclone's four original ZIP files, the WinFsp
# installer (both unchanged, with their original names), both license texts
# and release-notes.md. They are a backup only: CryptBridge downloads rclone
# from rclone.org first, and WinFsp is installed from its official page.
#
# Every file is checked against a SHA-256 known in advance, and the WinFsp
# installer must carry a valid signature. On any difference the script stops
# and leaves no half-finished folder behind.
#
# The rclone version and checksums are read from internal\prereq\prereq.go
# and linux\setup.sh, never kept a third time; the two must agree.
#
# Usage (from any folder):  powershell -File windows\release-assets.ps1

$ErrorActionPreference = 'Stop'
# The progress bar of Invoke-WebRequest slows big downloads down a lot in
# Windows PowerShell; each file is announced instead.
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

# --- WinFsp: the tested version (a stable release, not a beta) -------------
# From https://github.com/winfsp/winfsp/releases. The SHA-256 of the MSI is
# the one GitHub shows for the file; License.txt is taken from the same tag.
$winFspTag = 'v2.1'
$winFspMsi = 'winfsp-2.1.25156.msi'
$winFspMsiSha256 = '073a70e00f77423e34bed98b86e600def93393ba5822204fac57a29324db9f7a'
$winFspLicenseSha256 = '68ae2fa9d8e6110ebdc9a236beecf118e279f8a26530daf09c9e682c231f7879'

# --- rclone's license text (COPYING) of the pinned version -----------------
# Check it again when the rclone version changes.
$rcloneLicenseSha256 = '8cd2e9e750b90a04b7d82dbbca3930c696ae0309d7c10464f90a44f45754cd04'

$repoRoot = Split-Path $PSScriptRoot -Parent
$binDir = Join-Path $PSScriptRoot 'build\bin'
$releaseDir = Join-Path $binDir 'release'
$stagingDir = Join-Path $binDir 'release.partial'

# Get-SingleMatch returns the first group of the one match of pattern in
# text; no match or more than one is an error.
function Get-SingleMatch([string]$text, [string]$pattern, [string]$what) {
    $found = [regex]::Matches($text, $pattern, 'Multiline')
    if ($found.Count -ne 1) {
        throw "$what found $($found.Count) times, expected once"
    }
    return $found[0].Groups[1].Value
}

# Read-RclonePin reads the rclone version and the four ZIP checksums from
# prereq.go and setup.sh and makes sure both name the same version.
function Read-RclonePin {
    $goFile = Join-Path $PSScriptRoot 'internal\prereq\prereq.go'
    $shFile = Join-Path $repoRoot 'linux\setup.sh'
    $go = [IO.File]::ReadAllText($goFile)
    $sh = [IO.File]::ReadAllText($shFile)

    $goVersion = Get-SingleMatch $go '^const RcloneVersion = "(\d+\.\d+\.\d+)"' 'RcloneVersion in prereq.go'
    $shVersion = Get-SingleMatch $sh '^readonly RCLONE_VERSION="(\d+\.\d+\.\d+)"' 'RCLONE_VERSION in setup.sh'
    if ($goVersion -ne $shVersion) {
        throw "prereq.go pins rclone $goVersion, setup.sh ${shVersion}: they must be the same"
    }
    $hex = '([0-9a-f]{64})'
    return @{
        Version = $goVersion
        Sha256  = [ordered]@{
            'windows-amd64' = Get-SingleMatch $go "`"windows-amd64`": `"$hex`"" 'windows-amd64 checksum in prereq.go'
            'windows-arm64' = Get-SingleMatch $go "`"windows-arm64`": `"$hex`"" 'windows-arm64 checksum in prereq.go'
            'linux-amd64'   = Get-SingleMatch $sh "^readonly RCLONE_SHA256_LINUX_AMD64=`"$hex`"" 'linux-amd64 checksum in setup.sh'
            'linux-arm64'   = Get-SingleMatch $sh "^readonly RCLONE_SHA256_LINUX_ARM64=`"$hex`"" 'linux-arm64 checksum in setup.sh'
        }
    }
}

# Save-Checked downloads url into folder\name and stops unless the file has
# the SHA-256 expected.
function Save-Checked([string]$url, [string]$name, [string]$expected) {
    Write-Host "Downloading $name"
    Write-Host "  from $url"
    $target = Join-Path $stagingDir $name
    Invoke-WebRequest -Uri $url -OutFile $target -UseBasicParsing
    $actual = (Get-FileHash $target -Algorithm SHA256).Hash.ToLower()
    if ($actual -ne $expected) {
        throw "$name has SHA-256 $actual, expected $expected"
    }
    Write-Host ("  ok: {0:N0} bytes, SHA-256 matches" -f (Get-Item $target).Length)
    return $target
}

# Write-ReleaseNotes writes the backup part of the release text (Markdown,
# UTF-8 without BOM). The dot is written as a character code, because
# Windows PowerShell would misread it in this file.
function Write-ReleaseNotes([string]$rcloneVersion) {
    $dot = [char]0x00B7
    $winFspSource = "https://github.com/winfsp/winfsp/tree/$winFspTag"
    # Each line in parentheses: the comma binds more strongly than "+".
    $lines = @(
        'You only need CryptBridge.exe. Everything else is an optional backup.',
        '',
        'Backup copies, unchanged originals:',
        '',
        ("- rclone $rcloneVersion (ZIP files). Official: https://rclone.org/downloads/ $dot " +
            "Source: https://github.com/rclone/rclone/tree/v$rcloneVersion $dot MIT License (rclone-LICENSE.txt)"),
        ("- WinFsp $winFspTag (MSI installer). Install it only if WinFsp is not installed yet. " +
            "Official: https://winfsp.dev/rel/ $dot Source: $winFspSource $dot " +
            "WinFsp - Windows File System Proxy, Copyright (C) Bill Zissimopoulos $dot " +
            'GPLv3 with FLOSS exception (WinFsp-LICENSE.txt)'),
        '',
        ('Did you use an rclone of your own so far (on the PATH or next to CryptBridge.exe)? CryptBridge now ' +
            "uses exactly rclone ${rcloneVersion}: click Download rclone once, and if you use the LAN access, " +
            'allow it in the Windows firewall once more.')
    )
    $text = ($lines -join "`n") + "`n"
    [IO.File]::WriteAllText((Join-Path $stagingDir 'release-notes.md'), $text, [Text.UTF8Encoding]::new($false))
}

if (Test-Path $stagingDir) {
    Remove-Item $stagingDir -Recurse -Force
}
New-Item -ItemType Directory -Path $stagingDir | Out-Null
try {
    $pin = Read-RclonePin
    $version = $pin.Version
    Write-Host "rclone $version (prereq.go and setup.sh agree), WinFsp $winFspTag"

    foreach ($platform in $pin.Sha256.Keys) {
        $zip = "rclone-v$version-$platform.zip"
        [void](Save-Checked "https://downloads.rclone.org/v$version/$zip" $zip $pin.Sha256[$platform])
    }
    [void](Save-Checked "https://raw.githubusercontent.com/rclone/rclone/v$version/COPYING" 'rclone-LICENSE.txt' $rcloneLicenseSha256)

    $msi = Save-Checked "https://github.com/winfsp/winfsp/releases/download/$winFspTag/$winFspMsi" $winFspMsi $winFspMsiSha256
    $signature = Get-AuthenticodeSignature $msi
    if ($signature.Status -ne 'Valid') {
        throw "the signature of $winFspMsi is $($signature.Status), expected Valid"
    }
    Write-Host "  signature Valid, signed by: $($signature.SignerCertificate.Subject)"
    [void](Save-Checked "https://raw.githubusercontent.com/winfsp/winfsp/$winFspTag/License.txt" 'WinFsp-LICENSE.txt' $winFspLicenseSha256)

    Write-ReleaseNotes $version

    if (Test-Path $releaseDir) {
        Remove-Item $releaseDir -Recurse -Force
    }
    Rename-Item $stagingDir 'release'
}
catch {
    if (Test-Path $stagingDir) {
        Remove-Item $stagingDir -Recurse -Force
    }
    throw
}

Write-Host ''
Write-Host "Ready in $releaseDir (attach everything but release-notes.md; its text is the release text):"
Get-ChildItem $releaseDir | ForEach-Object {
    Write-Host ("  {0,-40} {1,10:N0} bytes  {2}" -f $_.Name, $_.Length, (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower())
}
