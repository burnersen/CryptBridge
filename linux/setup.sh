#!/usr/bin/env bash
# CryptBridge (https://github.com/burnersen/CryptBridge)
# Copyright (C) 2026 burnersen
# SPDX-License-Identifier: GPL-3.0-only
#
# CryptBridge Linux setup.
#
# Turns a fresh Ubuntu/Debian server into a streaming server for rclone-crypt
# encrypted vaults in pCloud, Google Drive, OneDrive, Dropbox or MEGA, one
# folder per vault:
#
#   cloud <-> rclone mount, one per vault (rclone-mount-N.service) -> ~/CryptBridge/NAME (read/write in the desktop)
#   ~/CryptBridge -> rclone serve webdav (rclone-webdav.service, read-only) <- Caddy (HTTPS) <- video player
#
# Run it again at any time to repair or change the setup. After the first run
# it is also installed as the command "cryptbridge" (status, vaults, ...).
#
#   sudo bash setup.sh            set up or change CryptBridge
#   cryptbridge                   menu: status, vaults, streaming settings, restart, ...
#   sudo cryptbridge vault add    add a vault (transfer block from the Windows program)
#   sudo cryptbridge uninstall    remove CryptBridge (keeps your files in the cloud)

set -Eeuo pipefail
# Tool output is parsed below; it must not depend on the system language.
export LC_ALL=C

readonly CRYPTBRIDGE_VERSION="0.11.0"
# The one rclone CryptBridge uses: everything is tested with exactly this
# version, and the Windows program pins the same one. The checksums are those
# of rclone's official ZIP files, from rclone's PGP-signed SHA256SUMS list.
readonly RCLONE_VERSION="1.75.2"
readonly RCLONE_SHA256_LINUX_AMD64="349ac8fba6ff65d6247043f1750cdcb518ec5d500ef91463a10d37c0ccdf3702"
readonly RCLONE_SHA256_LINUX_ARM64="7e1e8d69654941b7b7df84ee74c5f7fb09cce7d5496947fb7bf16b55ff427d10"

# --- Files and folders ---------------------------------------------------
# CB_TEST_ROOT moves every system path below a test folder; only the tests
# set it. In real use it is empty.
readonly ROOT="${CB_TEST_ROOT:-}"
readonly CONF_DIR="$ROOT/etc/cryptbridge"
# Google Drive, OneDrive and Dropbox renew the sign-in about every hour, and
# rclone writes the new token back into its configuration through a new
# file next to it. So the configuration has a folder of its own that the
# desktop user (who runs rclone) may write to. CONF_DIR itself stays root's:
# root reads the settings file in there as commands.
readonly RCLONE_DIR="$CONF_DIR/rclone"
# Every vault has a configuration of its own, NUMBER.conf: rclone writes the
# whole file anew when it renews a sign-in, so two vaults sharing one file
# would overwrite each other's new sign-in. RCLONE_CONF is the file of the
# vault being worked on (see select_vault).
RCLONE_CONF=""
# The keys of a transfer block wait here until they are checked; only then
# do they become a vault's configuration.
readonly NEW_RCLONE_CONF="$RCLONE_DIR/new.conf"
readonly SINGLE_RCLONE_CONF="$RCLONE_DIR/rclone.conf" # CryptBridge 0.8: one vault only
readonly OLD_RCLONE_CONF="$CONF_DIR/rclone.conf"      # up to CryptBridge 0.7
readonly HTPASSWD="$CONF_DIR/htpasswd"
readonly SETTINGS_FILE="$CONF_DIR/settings"
# The web servers (nginx for a website, say) paused for streaming, one per
# line: "UNIT enabled|disabled", i.e. whether it started with the server.
# Not in the settings: the setup writes those at its very end, and a setup
# that stops halfway must not forget a paused website.
readonly PAUSED_WEB_SERVERS="$CONF_DIR/paused-web-servers"
# One settings file per vault, named by its number (name, cloud, folder,
# check code, on or off). No secrets in there either.
readonly VAULTS_DIR="$CONF_DIR/vaults"
# One cache folder per vault, named by its number as well.
readonly CACHE_DIR="$ROOT/var/cache/cryptbridge"
readonly UNIT_DIR="$ROOT/etc/systemd/system"
# One mount service per vault (rclone-mount-NUMBER.service), so that a vault
# can be off, or fail, while the others keep working.
readonly VAULT_UNIT_PREFIX="rclone-mount-"
readonly OLD_MOUNT_UNIT="$UNIT_DIR/rclone-mount.service" # CryptBridge 0.8: one vault only
readonly WEBDAV_UNIT="$UNIT_DIR/rclone-webdav.service"
readonly CADDYFILE="$ROOT/etc/caddy/Caddyfile"
readonly F2B_FILTER="$ROOT/etc/fail2ban/filter.d/cryptbridge-webdav.conf"
readonly F2B_JAIL="$ROOT/etc/fail2ban/jail.d/cryptbridge-webdav.conf"
readonly SELF_INSTALLED="$ROOT/usr/local/sbin/cryptbridge"
readonly RCLONE_BIN="$ROOT/usr/local/bin/rclone"
readonly MARKER="# Managed by CryptBridge"

# --- rclone --------------------------------------------------------------
readonly CLOUD_REMOTE="cbcloud"
readonly OLD_CLOUD_REMOTE="cbpcloud" # up to CryptBridge 0.7
readonly VAULT_REMOTE="cbvault"
# The cloud services the setup can sign in to, by rclone's backend names, and
# their titles; the same as in the Windows program.
readonly -A SERVICE_TITLES=([pcloud]="pCloud" [drive]="Google Drive" [onedrive]="OneDrive" [dropbox]="Dropbox" [mega]="MEGA")
readonly WEBDAV_ADDR="127.0.0.1:8080"
readonly CHECK_FILE=".cryptbridge-vault"
# A vault's name is the name of its folder; see valid_vault_name.
readonly VAULT_NAME_MAX=60
readonly VAULT_NAME_FORBIDDEN='/\"$%`'
readonly DEFAULT_VAULT_NAME="Vault"
# Key check, the same rules as the Windows program: a wrong key passes one
# encrypted name by chance in about 256 cases, eight names make that
# negligible. An encrypted file starts with a 32-byte header, and its content
# carries a 16-byte authenticator: only a larger file holds content, which
# opens with the right keys alone. Such a file is read completely.
readonly MAX_NAMES_TO_CHECK=8
readonly CRYPT_OVERHEAD_SIZE=48
readonly MAX_CONTENT_CHECK_SIZE=1048576
readonly FINGERPRINT_PROBE="cryptbridge/fingerprint"
readonly HOST_EU="eapi.pcloud.com"
readonly HOST_US="api.pcloud.com"

# Streaming defaults for stutter-free 4K playback; the same values as in the
# Windows program. The mount carries them because it is the part that talks
# to the cloud; the WebDAV server only reads from the mount.
readonly BUFFER_SIZE="128M"
readonly CHUNK_SIZE="64M"
readonly CHUNK_SIZE_LIMIT="1G"
readonly CACHE_MODE="full"
readonly READ_AHEAD="128M"
readonly DIR_CACHE_TIME="24h"
readonly POLL_INTERVAL="1m"
# MEGA tells rclone about no changes made elsewhere, so its mount looks at
# the folders again after a minute instead of a day. rclone keeps MEGA's
# folder tree in memory and up to date, so that costs no network.
readonly QUIET_DIR_CACHE_TIME="1m"
readonly DEFAULT_CACHE_MAX_GB=20

# bcrypt cost of the streaming password; see choose_webdav_account.
readonly BCRYPT_COST=10

# --- Transfer block from the Windows program -----------------------------
readonly TRANSFER_BEGIN="-----BEGIN CRYPTBRIDGE TRANSFER-----"
readonly TRANSFER_END="-----END CRYPTBRIDGE TRANSFER-----"
# Format 1 (up to CryptBridge 0.7) carries a pCloud sign-in, format 2 the
# settings of any cloud account.
readonly TRANSFER_FORMAT_PREFIX="cryptbridge-transfer-"
readonly TRANSFER_FORMAT_PCLOUD="${TRANSFER_FORMAT_PREFIX}1"
readonly TRANSFER_FORMAT_CLOUD="${TRANSFER_FORMAT_PREFIX}2"
readonly TRANSFER_ITERATIONS=600000
# The characters of a transfer code (as in the Windows program: no 0/O, 1/I).
readonly TRANSFER_CODE_ALPHABET="ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
# A mistyped code should not mean pasting the block again.
readonly TRANSFER_CODE_TRIES=3

readonly SUPPORTED_SYSTEMS="ubuntu 24.04, ubuntu 26.04, debian 12, debian 13"

# ==========================================================================
# Output and input helpers
# ==========================================================================

if [[ -t 1 ]]; then
	BOLD=$'\e[1m' GREEN=$'\e[32m' YELLOW=$'\e[33m' RED=$'\e[31m' CYAN=$'\e[36m' RESET=$'\e[0m'
else
	BOLD="" GREEN="" YELLOW="" RED="" CYAN="" RESET=""
fi

step() { printf '\n%s==> %s%s\n' "$BOLD$CYAN" "$*" "$RESET"; }
ok() { printf '%s  ✓ %s%s\n' "$GREEN" "$*" "$RESET"; }
info() { printf '    %s\n' "$*"; }
warn() { printf '%s  ! %s%s\n' "$YELLOW" "$*" "$RESET" >&2; }
die() {
	printf '\n%s  ✗ %s%s\n' "$RED" "$*" "$RESET" >&2
	exit 1
}

trap 'die "Unexpected error in line $LINENO (command: $BASH_COMMAND). Run the setup again — every step can be repeated safely."' ERR

# All questions read from the terminal, so that "curl ... | sudo bash" works.
TTY="/dev/tty"

# ask VAR "Question" "default"
ask() {
	local __var="$1" prompt="$2" default="${3:-}" answer
	if [[ -n "$default" ]]; then
		read -r -p "    $prompt [$default]: " answer <"$TTY"
		answer="${answer:-$default}"
	else
		read -r -p "    $prompt: " answer <"$TTY"
	fi
	printf -v "$__var" '%s' "$answer"
}

# ask_secret VAR "Question" — input is not shown
ask_secret() {
	local __var="$1" prompt="$2" answer
	read -r -s -p "    $prompt: " answer <"$TTY"
	printf '\n' >&2
	printf -v "$__var" '%s' "$answer"
}

# confirm "Question" [default y|n] — returns 0 for yes
confirm() {
	local prompt="$1" default="${2:-y}" answer hint="[Y/n]"
	[[ "$default" == "n" ]] && hint="[y/N]"
	read -r -p "    $prompt $hint: " answer <"$TTY"
	answer="${answer:-$default}"
	[[ "$answer" =~ ^[YyJj] ]]
}

# run_logged "description" command... — keeps long tool output out of sight
LOG_FILE="$ROOT/var/log/cryptbridge-setup.log"
run_logged() {
	local description="$1"
	shift
	info "$description …"
	if ! "$@" >>"$LOG_FILE" 2>&1; then
		die "$description failed. Details: $LOG_FILE"
	fi
}

# ==========================================================================
# Basics: root, system, user
# ==========================================================================

require_root() {
	[[ $EUID -eq 0 ]] && return
	local self
	self="$(readlink -f "$0" 2>/dev/null || true)"
	[[ -f "$self" ]] || die "Please download the script and run it with:  sudo bash setup.sh"
	# Remember the SSH port before sudo drops the environment.
	local ssh_port=""
	[[ -n "${SSH_CONNECTION:-}" ]] && ssh_port="$(awk '{print $4}' <<<"$SSH_CONNECTION")"
	exec sudo CB_SSH_PORT="$ssh_port" bash "$self" "$@"
}

check_system() {
	[[ -r /etc/os-release ]] || die "Cannot identify this system (/etc/os-release missing)."
	# shellcheck disable=SC1091
	. /etc/os-release
	local system="${ID:-?} ${VERSION_ID:-?}"
	if [[ ", $SUPPORTED_SYSTEMS," != *", $system,"* ]]; then
		warn "This system ($system) has not been tested. Tested: $SUPPORTED_SYSTEMS."
		confirm "Continue anyway?" n || exit 1
	fi
	command -v systemctl >/dev/null || die "systemd is required."
	case "$(uname -m)" in
	x86_64) ARCH="amd64" ;;
	aarch64) ARCH="arm64" ;;
	*) die "Unsupported processor: $(uname -m)" ;;
	esac
	ok "System: $system ($ARCH)"
}

# Streaming (Caddy on ports 80 and 443) is on unless it was switched off in
# the menu; settings files up to CryptBridge 0.8.2 do not mention it.
CB_STREAMING="on"

load_settings() {
	# Non-secret settings of an earlier run (user, address, sizes ...).
	if [[ -r "$SETTINGS_FILE" ]]; then
		# shellcheck disable=SC1090
		. "$SETTINGS_FILE"
	fi
}

save_settings() {
	# This file is read back with "." — every value is quoted with %q so that
	# a folder name with special characters can never run as a command.
	# No secrets in here, so the desktop user may read it (for "cryptbridge").
	# What belongs to one vault is in the vault's own file (save_vault); up to
	# CryptBridge 0.8 the folder and check code of the one vault were in here.
	{
		echo "$MARKER — settings of the last setup run and of the menu (no secrets here)"
		local name
		for name in CB_USER CB_MOUNT_DIR CB_ADDRESS CB_WEBDAV_USER CB_CACHE_MAX CB_CACHE_MIN_FREE CB_STREAMING; do
			printf '%s=%q\n' "$name" "${!name}"
		done
	} >"$SETTINGS_FILE"
	chmod 0644 "$SETTINGS_FILE"
}

choose_user() {
	step "Desktop user"
	local default="${CB_USER:-${SUDO_USER:-}}"
	while true; do
		ask CB_USER "Linux user who works with the vault in the desktop" "$default"
		if [[ "$CB_USER" == "root" ]]; then
			warn "Using root for the desktop is not recommended. Please name your normal user."
			continue
		fi
		# "id" takes a number as a user ID (2 is "bin"), but the services
		# need the name.
		if [[ "$CB_USER" =~ ^[0-9]+$ ]]; then
			warn "Please type the user's name, not a number."
			continue
		fi
		id "$CB_USER" >/dev/null 2>&1 && break
		warn "User '$CB_USER' does not exist."
	done
	load_user
	CB_MOUNT_DIR="${CB_MOUNT_DIR:-$CB_HOME/CryptBridge}"
	ok "Folder of the vaults for $CB_USER: $CB_MOUNT_DIR"
}

# load_user — the group and home folder of the desktop user CB_USER.
load_user() {
	CB_GROUP="$(id -gn "$CB_USER")"
	CB_HOME="$(getent passwd "$CB_USER" | cut -d: -f6)"
}

# ==========================================================================
# Packages, rclone, Caddy
# ==========================================================================

# apt_update refreshes the package lists. A broken source that has nothing to
# do with CryptBridge (for example a third-party repository without packages
# for this Ubuntu release) makes "apt-get update" fail as a whole; that must
# not stop the setup. Only a package that really cannot be installed does.
apt_update() {
	info "Updating package lists …"
	local out
	out="$(mktemp)"
	if apt-get update >"$out" 2>&1; then
		cat "$out" >>"$LOG_FILE"
		rm -f "$out"
		return 0
	fi
	cat "$out" >>"$LOG_FILE"
	warn "Some package sources could not be updated. CryptBridge does not need them; continuing."
	local line
	while IFS= read -r line; do
		warn "  $line"
	done < <(grep -E '^(E|Err):' "$out" || true)
	rm -f "$out"
}

install_packages() {
	step "Installing packages"
	export DEBIAN_FRONTEND=noninteractive
	apt_update
	# Installed one by one: a single unknown package must not stop the others.
	local package status
	for package in ca-certificates curl unzip jq openssl fuse3 ufw fail2ban apache2-utils; do
		# Read into a variable: "| grep -q" can fail falsely under pipefail.
		status="$(dpkg-query -W -f='${Status}' "$package" 2>/dev/null || true)"
		if [[ "$status" != "install ok installed" ]]; then
			run_logged "Installing $package" apt-get install -y "$package"
		fi
	done
	install_caddy
	ok "Packages ready"
}

install_caddy() {
	if ! command -v caddy >/dev/null; then
		# The distribution's package, on purpose: Caddy's own repository (on
		# Cloudsmith) answered "402 Payment Required" on 2026-10-10, and a
		# third-party source is one more thing that can break. Everything
		# CryptBridge uses (automatic HTTPS, reverse_proxy, JSON logs,
		# hash-password) works with the packaged version (2.6.2 on Ubuntu 26.04).
		run_logged "Installing caddy" apt-get install -y caddy
	fi
	ok "Caddy $(caddy version | awk '{print $1}')"
}

rclone_version() {
	local out
	out="$("$1" version 2>/dev/null)" || return 1
	[[ "$out" =~ rclone\ v([0-9]+\.[0-9]+\.[0-9]+) ]] || return 1
	printf '%s' "${BASH_REMATCH[1]}"
}

# rclone_sha256 ARCH — the built-in checksum of rclone's ZIP file for ARCH.
rclone_sha256() {
	case "$1" in
	amd64) printf '%s' "$RCLONE_SHA256_LINUX_AMD64" ;;
	arm64) printf '%s' "$RCLONE_SHA256_LINUX_ARM64" ;;
	*) return 1 ;;
	esac
}

# rclone_sources ZIP — where rclone's ZIP file is downloaded from, in this
# order: rclone's download server, rclone's release on GitHub and the
# unchanged copy kept with the CryptBridge release of this setup.sh.
rclone_sources() {
	printf '%s\n' \
		"https://downloads.rclone.org/v$RCLONE_VERSION/$1" \
		"https://github.com/rclone/rclone/releases/download/v$RCLONE_VERSION/$1" \
		"https://github.com/burnersen/CryptBridge/releases/download/v$CRYPTBRIDGE_VERSION/$1"
}

# download_verified FILE SHA256 URL... — downloads URL after URL into FILE
# until one file has the checksum SHA256. A file that does not match is
# deleted before the next try. Fails if no URL delivers the right file.
download_verified() {
	local file="$1" want="$2" url actual
	shift 2
	for url in "$@"; do
		if curl -fsSL --connect-timeout 30 --max-time 600 "$url" -o "$file"; then
			actual="$(sha256sum "$file" | awk '{print $1}')"
			if [[ "$actual" == "$want" ]]; then
				return 0
			fi
			warn "$url: the file does not match the checksum built into CryptBridge"
		else
			warn "$url: download failed"
		fi
		rm -f "$file"
	done
	return 1
}

# install_rclone puts exactly RCLONE_VERSION into RCLONE_BIN, in place of any
# other version. The services pick it up when start_services restarts them.
install_rclone() {
	step "rclone"
	local current=""
	if [[ -x "$RCLONE_BIN" ]]; then
		current="$(rclone_version "$RCLONE_BIN" || true)"
	fi
	if [[ "$current" == "$RCLONE_VERSION" ]]; then
		ok "rclone $current is installed"
		return
	fi

	local tmp zip want urls
	zip="rclone-v$RCLONE_VERSION-linux-$ARCH.zip"
	want="$(rclone_sha256 "$ARCH")" || die "CryptBridge has no rclone for this processor ($ARCH)."
	if [[ -n "$current" ]]; then
		info "Replacing rclone $current with $RCLONE_VERSION, the version CryptBridge is tested with …"
	else
		info "Downloading rclone $RCLONE_VERSION …"
	fi
	tmp="$(mktemp -d)"
	mapfile -t urls < <(rclone_sources "$zip")
	if ! download_verified "$tmp/$zip" "$want" "${urls[@]}"; then
		rm -rf "$tmp"
		die "rclone $RCLONE_VERSION could not be downloaded with the right checksum — nothing was installed."
	fi
	unzip -q -o "$tmp/$zip" -d "$tmp"
	install -m 0755 "$tmp/rclone-v$RCLONE_VERSION-linux-$ARCH/rclone" "$RCLONE_BIN"
	rm -rf "$tmp"
	ok "rclone $RCLONE_VERSION installed (checked against the checksum built into CryptBridge)"
}

# as_user runs a command as the desktop user, who runs rclone.
as_user() {
	runuser -u "$CB_USER" -- "$@"
}

# rc runs rclone as the desktop user with CryptBridge's configuration.
rc() {
	as_user "$RCLONE_BIN" --config "$RCLONE_CONF" "$@"
}

# obscure turns a password into rclone's obscured form, reading it from
# stdin so that it never appears in the process list.
obscure() {
	printf '%s' "$1" | "$RCLONE_BIN" obscure -
}

# ==========================================================================
# Cloud sign-in and vault keys
# ==========================================================================

# reset_keys — the values that one of the ways in get_keys fills, empty or at
# rclone's defaults, so that nothing of an earlier vault is taken by mistake.
# CLOUD_SETTINGS are rclone's settings of the cloud account, one
# "key = value" per line, without the type (that is CB_SERVICE). CB_VAULT_NAME
# is the vault's name in the Windows program, if the block brings one.
reset_keys() {
	CB_SERVICE=""
	CLOUD_SETTINGS=""
	PCLOUD_TOKEN=""
	CB_VAULT_NAME=""
	CB_VAULT_PATH=""
	VAULT_ENCRYPTION="standard"
	VAULT_DIRNAMES="true"
	VAULT_ENCODING="base32"
	VAULT_SUFFIX=".bin"
	VAULT_PASSWORD=""
	VAULT_SALT=""
	EXPECTED_FINGERPRINT=""
}
reset_keys

# A valid cloud sign-in in a transfer block: a service name, the settings
# that hold the sign-in (MEGA: a session, the others: a token), and settings
# that can only become "key = value" lines of the rclone configuration —
# a line break in a value could add settings or remotes of its own.
# shellcheck disable=SC2016 # jq's code, not the shell's
readonly VALID_CLOUD_SIGN_IN='
	(.service | type == "string" and test("^[a-z0-9]+$"))
	and (.settings | type == "object")
	and ((if .service == "mega" then ["session_id", "master_key"] else ["token"] end) as $signIn
		| .settings as $s | $signIn | all($s[.] | type == "string" and length > 0))
	and (.settings | to_entries | all(
		(.key | test("^[a-z0-9_]+$")) and .key != "type"
		and (.value | type == "string" and (test("[\r\n]") | not))))'

# service_title — the title of CB_SERVICE, e.g. "Google Drive"
service_title() {
	local service="${CB_SERVICE:-}"
	if [[ -n "$service" && -n "${SERVICE_TITLES[$service]:-}" ]]; then
		printf '%s' "${SERVICE_TITLES[$service]}"
	else
		printf 'the cloud'
	fi
}

# get_keys reads the cloud sign-in and the keys of one vault.
get_keys() {
	step "Cloud sign-in and vault keys"
	reset_keys
	info "How do you want to bring the keys of the vault to this server?"
	info "  1) Paste the transfer block from the CryptBridge Windows program (recommended)"
	info "  2) Read the transfer block from a file"
	info "  3) Type everything by hand (pCloud only)"
	local choice
	ask choice "Your choice" "1"
	case "$choice" in
	1) read_transfer_block "" ;;
	2)
		local file
		ask file "Path of the transfer file"
		[[ -r "$file" ]] || die "Cannot read $file"
		read_transfer_block "$file"
		;;
	3) read_keys_by_hand ;;
	*) die "Please choose 1, 2 or 3." ;;
	esac
}

read_transfer_block() {
	local file="$1" text line code json
	if [[ -z "$file" ]]; then
		info "Paste the block now (it starts with $TRANSFER_BEGIN):"
		text=""
		while IFS= read -r line <"$TTY"; do
			text+="$line"$'\n'
			[[ "$line" == *"$TRANSFER_END"* ]] && break
		done
	else
		text="$(<"$file")"
	fi
	local body
	body="$(transfer_body "$text")"
	[[ -n "$body" ]] || die "No transfer block found."

	local try left tries_word
	for ((try = 1; try <= TRANSFER_CODE_TRIES; try++)); do
		ask_secret code "Transfer code (shown in the Windows program)"
		if json="$(open_transfer_block "$body" "$code")"; then
			take_transfer "$json"
			ok "Transfer block opened: vault '$CB_VAULT_PATH' in $(service_title), check code $EXPECTED_FINGERPRINT"
			return
		fi
		left=$((TRANSFER_CODE_TRIES - try))
		if ((left > 0)); then
			tries_word="tries"
			if ((left == 1)); then tries_word="try"; fi
			warn "Wrong transfer code (or the block is incomplete). Please type it again ($left $tries_word left)."
		fi
	done
	die "Wrong transfer code (or the block is incomplete). Block and code belong together: use the code shown next to the block you pasted."
}

# transfer_body TEXT — prints the lines between the markers of the block in
# TEXT (copied text may carry Windows line ends).
transfer_body() {
	sed -n "/$TRANSFER_BEGIN/,/$TRANSFER_END/p" <<<"$1" | sed '1d;$d' | tr -d '\r'
}

# open_transfer_block BODY CODE — prints the content of the block, whose
# lines between the markers are BODY; fails if CODE does not open it.
open_transfer_block() {
	local json
	json="$(printf '%s\n' "$1" | CB_CODE="$(normalise_code "$2")" openssl enc -d -aes-256-cbc -pbkdf2 \
		-iter "$TRANSFER_ITERATIONS" -md sha256 -a -pass env:CB_CODE 2>/dev/null | tr -d '\0')" || return 1
	# openssl takes a wrong code in about 1 of 256 cases and returns garbage.
	jq -e --arg prefix "$TRANSFER_FORMAT_PREFIX" '.format | strings | startswith($prefix)' \
		<<<"$json" >/dev/null 2>&1 || return 1
	printf '%s\n' "$json"
}

# normalise_code CODE — keeps only the characters a transfer code is made of,
# upper case. So lower case, spaces, dashes and the invisible characters that
# copying can bring along (a carriage return, a non-breaking space) do not
# matter. The Windows program does the same.
normalise_code() {
	printf '%s' "$1" | LC_ALL=C tr 'a-z' 'A-Z' | LC_ALL=C tr -cd "$TRANSFER_CODE_ALPHABET"
}

# take_transfer JSON — takes the cloud sign-in and the vault keys from an
# opened transfer block.
take_transfer() {
	local json="$1" format cloud
	format="$(jq -r '.format' <<<"$json" 2>/dev/null)" || format=""
	case "$format" in
	"$TRANSFER_FORMAT_CLOUD") cloud="$(jq -c '.cloud' <<<"$json")" ;;
	"$TRANSFER_FORMAT_PCLOUD") cloud="$(jq -c '{service: "pcloud", settings: {hostname: .pcloud.hostname, token: .pcloud.token}}' <<<"$json")" ;;
	"$TRANSFER_FORMAT_PREFIX"*) die "This transfer block comes from a newer CryptBridge. Download the current setup.sh (see the README) and run it." ;;
	*) die "Wrong transfer code (or the block is incomplete)." ;;
	esac
	take_cloud_sign_in "$cloud"

	# Blocks of CryptBridge up to 0.8 carry no name.
	CB_VAULT_NAME="$(jq -r '.name // ""' <<<"$json")"
	CB_VAULT_PATH="$(jq -r '.vault.path' <<<"$json")"
	VAULT_ENCRYPTION="$(jq -r '.vault.filenameEncryption' <<<"$json")"
	VAULT_DIRNAMES="$(jq -r '.vault.directoryNameEncryption' <<<"$json")"
	VAULT_ENCODING="$(jq -r '.vault.filenameEncoding' <<<"$json")"
	VAULT_SUFFIX="$(jq -r '.vault.suffix' <<<"$json")"
	VAULT_PASSWORD="$(jq -r '.vault.password' <<<"$json")"
	VAULT_SALT="$(jq -r '.vault.password2' <<<"$json")"
	EXPECTED_FINGERPRINT="$(jq -r '.fingerprint' <<<"$json")"
}

# take_cloud_sign_in JSON — {"service": ..., "settings": {...}} into
# CB_SERVICE and CLOUD_SETTINGS.
take_cloud_sign_in() {
	local cloud="$1"
	jq -e "$VALID_CLOUD_SIGN_IN" <<<"$cloud" >/dev/null 2>&1 ||
		die "The transfer block holds no valid cloud sign-in. Create a new block in the Windows program."
	CB_SERVICE="$(jq -r '.service' <<<"$cloud")"
	[[ -n "${SERVICE_TITLES[$CB_SERVICE]:-}" ]] ||
		die "This setup cannot sign in to '$CB_SERVICE'. Download the current setup.sh (see the README) and run it."
	CLOUD_SETTINGS="$(jq -r '.settings | to_entries[] | "\(.key) = \(.value)"' <<<"$cloud")"
}

read_keys_by_hand() {
	info "pCloud sign-in: on a PC with a browser run   rclone authorize \"pcloud\"   ,"
	info "sign in, and paste the line that starts with {\"access_token\" here."
	info "(Google Drive, OneDrive, Dropbox and MEGA: please use the transfer block.)"
	ask_secret PCLOUD_TOKEN "pCloud token"
	[[ "$PCLOUD_TOKEN" == '{"access_token"'* ]] || die "That does not look like an rclone pCloud token."
	CB_SERVICE="pcloud"
	CLOUD_SETTINGS="" # configure_remotes finds the pCloud server
	ask CB_VAULT_PATH "Folder of the vault in pCloud" "CryptBridge"
	ask_secret VAULT_PASSWORD "Encryption password"
	local again
	ask_secret again "Encryption password again"
	[[ "$VAULT_PASSWORD" == "$again" ]] || die "The passwords differ."
	ask_secret VAULT_SALT "Salt (second password; leave empty if your vault has none)"
	if confirm "Was the vault created with other than the standard rclone settings?" n; then
		ask VAULT_ENCRYPTION "File name encryption (standard/obfuscate/off)" "$VAULT_ENCRYPTION"
		ask VAULT_DIRNAMES "Encrypt folder names (true/false)" "$VAULT_DIRNAMES"
		ask VAULT_ENCODING "File name encoding (base32/base64/base32768)" "$VAULT_ENCODING"
		ask VAULT_SUFFIX "File ending (only with 'off')" "$VAULT_SUFFIX"
	fi
}

# write_rclone_conf — writes the cloud remote (CB_SERVICE with
# CLOUD_SETTINGS) and the vault remote on top of it.
write_rclone_conf() {
	local pw1 pw2=""
	pw1="$(obscure "$VAULT_PASSWORD")"
	[[ -n "$VAULT_SALT" ]] && pw2="$(obscure "$VAULT_SALT")"
	prepare_rclone_dir
	{
		echo "$MARKER — contains your cloud sign-in and vault keys. Keep it secret."
		echo "[$CLOUD_REMOTE]"
		echo "type = $CB_SERVICE"
		printf '%s\n' "$CLOUD_SETTINGS"
		echo
		echo "[$VAULT_REMOTE]"
		echo "type = crypt"
		echo "remote = $CLOUD_REMOTE:$(normalise_path "$CB_VAULT_PATH")"
		echo "filename_encryption = $VAULT_ENCRYPTION"
		echo "directory_name_encryption = $VAULT_DIRNAMES"
		echo "filename_encoding = $VAULT_ENCODING"
		echo "suffix = $VAULT_SUFFIX"
		echo "password = $pw1"
		if [[ -n "$pw2" ]]; then echo "password2 = $pw2"; fi
	} | write_private "$RCLONE_CONF"
}

# prepare_rclone_dir — the folder of the rclone configuration, which only
# the desktop user can enter (see RCLONE_DIR). Files of an earlier desktop
# user change hands too; -h keeps chown on links themselves.
prepare_rclone_dir() {
	install -d -m 0750 -o root -g "$CB_GROUP" "$CONF_DIR"
	install -d -m 0700 -o "$CB_USER" -g "$CB_GROUP" "$RCLONE_DIR"
	chown -hR "$CB_USER:$CB_GROUP" "$RCLONE_DIR"
}

# write_private FILE — writes stdin into FILE as the desktop user, readable
# by nobody else. Root never writes into the user's folder itself, so a link
# placed there cannot turn root's writing to another file.
write_private() {
	as_user sh -c 'umask 077 && rm -f "$1" && cat >"$1"' sh "$1"
}

# copy_old_rclone_conf — up to CryptBridge 0.7 the configuration lay in
# CONF_DIR, and the cloud remote had pCloud's name. It is copied to its own
# folder under the new name, where CryptBridge 0.8 kept it, and from there
# update_single_vault takes it on. The old file stays until the services use
# the new one (remove_old_rclone_conf): a setup ended before that leaves them
# working as they were.
copy_old_rclone_conf() {
	[[ -f "$OLD_RCLONE_CONF" ]] || return 0
	prepare_rclone_dir
	[[ -s "$SINGLE_RCLONE_CONF" ]] && return 0
	sed -e "s/^\[$OLD_CLOUD_REMOTE\]\$/[$CLOUD_REMOTE]/" \
		-e "s/^remote = $OLD_CLOUD_REMOTE:/remote = $CLOUD_REMOTE:/" "$OLD_RCLONE_CONF" | write_private "$SINGLE_RCLONE_CONF"
	ok "rclone configuration copied to $RCLONE_DIR"
}

remove_old_rclone_conf() {
	rm -f "$OLD_RCLONE_CONF"
}

normalise_path() {
	local p="${1//\\//}"
	p="${p#"${p%%[!/ ]*}"}"
	p="${p%"${p##*[!/ ]}"}"
	printf '%s' "$p"
}

configure_remotes() {
	if [[ -n "$CLOUD_SETTINGS" ]]; then
		write_rclone_conf
		check_cloud_access
		return
	fi
	# pCloud typed by hand: the region is not known, try EU first.
	local host
	for host in "$HOST_EU" "$HOST_US"; do
		CLOUD_SETTINGS="hostname = $host"$'\n'"token = $PCLOUD_TOKEN"
		write_rclone_conf
		if rc about "$CLOUD_REMOTE:" >/dev/null 2>&1; then
			break
		fi
	done
	check_cloud_access
}

# read_kept_keys — what the later steps need from the kept configuration.
read_kept_keys() {
	CB_SERVICE="$(conf_value "$CLOUD_REMOTE" type)"
	CB_VAULT_PATH="$(conf_value "$VAULT_REMOTE" remote)"
	CB_VAULT_PATH="${CB_VAULT_PATH#*:}"
	VAULT_ENCRYPTION="$(conf_value "$VAULT_REMOTE" filename_encryption)"
	VAULT_DIRNAMES="$(conf_value "$VAULT_REMOTE" directory_name_encryption)"
}

# conf_value REMOTE KEY — prints the setting KEY of REMOTE in the rclone
# configuration.
conf_value() {
	awk -v section="[$1]" -v key="$2" '
		$0 == section { inside = 1; next }
		/^\[/ { inside = 0 }
		inside && index($0, key " = ") == 1 { print substr($0, length(key) + 4); exit }' "$RCLONE_CONF"
}

# check_cloud_access — the cloud must accept the sign-in; stops otherwise.
check_cloud_access() {
	local free
	free="$(reach_cloud)" || die "$free"
	ok "$(service_title) reached${free:+: free $free}"
}

# reach_cloud — asks the cloud of RCLONE_CONF for its free space and prints
# it; if the cloud does not answer, prints why in words and fails. The parts
# of rclone's messages are the ones the Windows program uses (see
# windows/internal/cloud/problem.go and pcloud.go); a missing network comes
# first, as a token renewal can fail for want of it too.
reach_cloud() {
	local out title
	title="$(service_title)"
	if out="$(rc about "$CLOUD_REMOTE:" 2>&1)"; then
		awk '/^Free:/{print $2 " " $3}' <<<"$out"
		return 0
	fi
	case "$out" in
	*"dial tcp"* | *"i/o timeout"* | *"TLS handshake timeout"*)
		printf '%s cannot be reached. Please check the internet connection of this server: %s' "$title" "$out" ;;
	*"(2094)"* | *"(2095)"* | *invalid_grant* | *"Error 401"* | *InvalidAuthenticationToken* | *invalid_access_token* | *expired_access_token* | \
		*"Invalid or expired user session"* | *"couldn't decode master key"*)
		printf '%s no longer accepts this sign-in. Sign in again (in the Windows program) and bring the keys again.' "$title" ;;
	*invalid_client* | *unauthorized_client*)
		printf '%s does not accept your own app. Check its client ID and secret in the Windows program and bring the keys again.' "$title" ;;
	*) printf '%s cannot be reached: %s' "$title" "$out" ;;
	esac
	return 1
}

# ==========================================================================
# Key check and check code — the same rules as in the Windows program
# ==========================================================================

verify_keys() {
	step "Checking the keys"
	local path raw check_name opened checked
	path="$(normalise_path "$CB_VAULT_PATH")"
	if ! raw="$(rc lsf "$CLOUD_REMOTE:$path" 2>/dev/null)" || [[ -z "$raw" ]]; then
		warn "The folder '$path' is empty or does not exist yet."
		confirm "Create a NEW vault there with these keys?" n || die "Nothing changed. Check the folder name."
		rc mkdir "$VAULT_REMOTE:"
		printf 'This file lets CryptBridge verify the keys of this vault.\r\nDeleting it does no harm to your files, but please keep it.\r\n' |
			rc rcat "$VAULT_REMOTE:$CHECK_FILE"
		ok "New vault created"
		return
	fi

	if [[ "$VAULT_ENCRYPTION" != "standard" ]]; then
		# Obfuscated and plain names come out of any keys, the name of the
		# check file too. Only the content proves the keys.
		verify_by_content "$path" "$raw"
		return
	fi
	check_name="$(rc backend encode "$VAULT_REMOTE:" "$CHECK_FILE")"
	if [[ $'\n'"$raw"$'\n' == *$'\n'"$check_name"$'\n'* ]]; then
		ok "The keys open this vault (check file found)"
		return
	fi
	read -r opened checked < <(decrypt_names "$raw")
	if ((checked > 0 && opened == checked)); then
		ok "The keys open this vault ($opened of $checked names decrypted)"
		return
	fi
	die "These keys do NOT open the vault in '$path' ($opened of $checked names). Check password, salt and settings — nothing was changed."
}

# verify_by_content PATH RAW — for vaults whose names prove nothing: the
# names must still follow the vault's name settings (the content proves only
# password and salt), and one file must open with the keys.
verify_by_content() {
	local path="$1" raw="$2" opened checked sample
	read -r opened checked < <(decrypt_names "$raw")
	if ((opened < checked)); then
		die "These keys do NOT open the vault in '$path' ($opened of $checked names fit its name settings). Check the settings — nothing was changed."
	fi
	sample="$(content_sample "$path")"
	if [[ -z "$sample" ]]; then
		warn "The keys cannot be checked: the file names of this vault prove nothing, and it holds no small file to test the keys with."
		confirm "Continue anyway?" n || die "Nothing changed."
		return
	fi
	if opens_content "$sample"; then
		ok "The keys open this vault (a file was decrypted)"
		return
	fi
	die "These keys do NOT open the vault in '$path' (a file did not decrypt). Check password, salt and settings — nothing was changed."
}

# decrypt_names RAW — tries to decrypt up to MAX_NAMES_TO_CHECK of the listed
# names and prints "opened checked". Plain folder names prove nothing.
decrypt_names() {
	local name names opened=0 checked=0
	mapfile -t names <<<"$1"
	for name in "${names[@]}"; do
		[[ -z "$name" ]] && continue
		((checked < MAX_NAMES_TO_CHECK)) || break
		[[ "$name" == */ ]] && ! folder_names_encrypted && continue
		checked=$((checked + 1))
		if rc backend decode "$VAULT_REMOTE:" "${name%/}" >/dev/null 2>&1; then
			opened=$((opened + 1))
		fi
	done
	printf '%s %s\n' "$opened" "$checked"
}

# folder_names_encrypted — with file name encryption off, rclone leaves
# folder names plain whatever the folder setting says.
folder_names_encrypted() {
	[[ "$VAULT_DIRNAMES" == "true" && "$VAULT_ENCRYPTION" != "off" ]]
}

# content_sample PATH — prints the stored name of the smallest file that can
# prove the keys: larger than an empty encrypted file, and small enough to
# be read completely. Prints nothing if there is none.
content_sample() {
	rc lsf --files-only --format sp --separator $'\t' "$CLOUD_REMOTE:$1" |
		awk -v overhead="$CRYPT_OVERHEAD_SIZE" -v max="$MAX_CONTENT_CHECK_SIZE" '
			{ size = substr($0, 1, index($0, "\t") - 1) + 0; name = substr($0, index($0, "\t") + 1) }
			size > overhead && size <= max && (best == "" || size < best_size) { best = name; best_size = size }
			END { if (best != "") print best }'
}

# opens_content NAME — succeeds if the keys decrypt the file stored as NAME
# in the vault folder, fails for wrong keys, and stops the setup on any
# other problem. The variant of the vault remote reads the stored name as it
# is and decrypts only the content. (The Windows program needs a remote of
# its own for this because its rclone keeps remotes in a cache; here every
# rclone call is a new process.)
opens_content() {
	local out
	if out="$(rc md5sum --download "$VAULT_REMOTE,filename_encryption=off,suffix=none:$1" 2>&1)"; then
		return 0
	fi
	[[ "$out" == *"failed to authenticate decrypted block"* ]] && return 1
	die "A file of the vault could not be read to check the keys: $out"
}

# Same as the Windows program: sha256(path + "\n" + encrypted probe), first
# 8 hex digits, upper case, as XXXX-XXXX.
compute_fingerprint() {
	local path encoded hash
	path="$(normalise_path "$CB_VAULT_PATH")"
	if [[ "$VAULT_ENCRYPTION" == "off" ]]; then
		CB_FINGERPRINT="(not available)"
		return
	fi
	encoded="$(rc backend encode "$VAULT_REMOTE:" "$FINGERPRINT_PROBE")"
	hash="$(printf '%s\n%s' "$path" "$encoded" | sha256sum | cut -c1-8 | tr 'a-f' 'A-F')"
	CB_FINGERPRINT="${hash:0:4}-${hash:4:4}"
	if [[ -n "$EXPECTED_FINGERPRINT" && "$EXPECTED_FINGERPRINT" != "$CB_FINGERPRINT" ]]; then
		die "Check code $CB_FINGERPRINT differs from the Windows program ($EXPECTED_FINGERPRINT). The keys differ — nothing was started."
	fi
	ok "Check code: $BOLD$CB_FINGERPRINT$RESET (must be the same in the Windows program)"
}

# ==========================================================================
# Vaults: several on one server, each in a folder of its own
# ==========================================================================

# The vault being worked on. Its file in VAULTS_DIR holds VAULT_SETTINGS;
# select_vault reads them and points RCLONE_CONF to its configuration.
CB_VAULT_ID=""
CB_VAULT_STATE="on"
readonly VAULT_SETTINGS=(CB_VAULT_NAME CB_SERVICE CB_VAULT_PATH CB_FINGERPRINT CB_VAULT_STATE)

# vault_ids — the numbers of the vaults on this server, in order.
vault_ids() {
	local file
	for file in "$VAULTS_DIR"/*; do
		if [[ -f "$file" && "${file##*/}" =~ ^[0-9]+$ ]]; then
			printf '%s\n' "${file##*/}"
		fi
	done | sort -n
}

# next_vault_id — the number of a new vault: one more than the highest.
next_vault_id() {
	local last
	last="$(vault_ids | tail -n1)"
	echo $((${last:-0} + 1))
}

# select_vault ID — makes vault ID the one being worked on.
select_vault() {
	CB_VAULT_ID="$1"
	RCLONE_CONF="$RCLONE_DIR/$1.conf"
	# shellcheck disable=SC1090
	. "$VAULTS_DIR/$1"
}

# save_vault — writes the file of the selected vault. Like the settings, it
# is read back with ".", so every value is quoted with %q.
save_vault() {
	install -d -m 0755 "$VAULTS_DIR"
	{
		echo "$MARKER — vault $CB_VAULT_ID (no secrets here)"
		local name
		for name in "${VAULT_SETTINGS[@]}"; do
			printf '%s=%q\n' "$name" "${!name}"
		done
	} >"$VAULTS_DIR/$CB_VAULT_ID"
	chmod 0644 "$VAULTS_DIR/$CB_VAULT_ID"
}

# vault_name ID — the name of vault ID; the selected vault stays selected.
vault_name() {
	(
		# shellcheck disable=SC1090
		. "$VAULTS_DIR/$1"
		printf '%s' "$CB_VAULT_NAME"
	)
}

# vault_names — the names of all vaults, for messages.
vault_names() {
	local id text=""
	for id in $(vault_ids); do
		text+="${text:+, }'$(vault_name "$id")'"
	done
	printf '%s' "${text:-none}"
}

# vault_identity ID — what makes vault ID the same vault as the keys of a
# transfer block: cloud service, folder in the cloud and check code.
vault_identity() {
	(
		# shellcheck disable=SC1090
		. "$VAULTS_DIR/$1"
		printf '%s|%s|%s' "$CB_SERVICE" "$(normalise_path "$CB_VAULT_PATH")" "$CB_FINGERPRINT"
	)
}

# The folder and the service of the selected vault.
vault_dir() { printf '%s/%s' "$CB_MOUNT_DIR" "$CB_VAULT_NAME"; }
vault_unit() { printf '%s%s.service' "$VAULT_UNIT_PREFIX" "$CB_VAULT_ID"; }

# valid_vault_name NAME — the name becomes a folder in CB_MOUNT_DIR, a folder
# of the video players' share and part of the vault's service file. So: not
# empty, not too long, not hidden (a leading dot), no spaces around it, no
# control characters, no "/" (a folder in a folder), and none of the
# characters that systemd reads differently in quotes (VAULT_NAME_FORBIDDEN).
valid_vault_name() {
	local name="$1" i
	[[ -n "$name" && ${#name} -le $VAULT_NAME_MAX ]] || return 1
	[[ "$name" != [.\ ]* && "$name" != *" " ]] || return 1
	[[ ! "$name" =~ [[:cntrl:]] ]] || return 1
	for ((i = 0; i < ${#VAULT_NAME_FORBIDDEN}; i++)); do
		[[ "$name" != *"${VAULT_NAME_FORBIDDEN:i:1}"* ]] || return 1
	done
}

# vault_name_taken NAME [ID] — true if a vault other than ID is called NAME.
# Upper and lower case count as the same, as they do on a Mac or Apple TV.
vault_name_taken() {
	local wanted="${1,,}" id other
	for id in $(vault_ids); do
		[[ "$id" == "${2:-}" ]] && continue
		other="$(vault_name "$id")"
		[[ "${other,,}" == "$wanted" ]] && return 0
	done
	return 1
}

# suggested_vault_name — the vault's name in the Windows program; for a block
# of an older version, or keys typed by hand, the last part of its folder in
# the cloud.
suggested_vault_name() {
	local folder
	folder="$(normalise_path "$CB_VAULT_PATH")"
	folder="${folder##*/}"
	printf '%s' "${CB_VAULT_NAME:-${folder:-$DEFAULT_VAULT_NAME}}"
}

# choose_vault_name SUGGESTION — asks for the name of the selected vault,
# which is the name of its folder.
choose_vault_name() {
	local suggestion="$1" own=""
	# A vault being named again (see update_single_vault) has its folder already.
	if [[ -f "$VAULTS_DIR/$CB_VAULT_ID" ]]; then
		own="$(vault_name "$CB_VAULT_ID")"
	fi
	if ! valid_vault_name "$suggestion" || vault_name_taken "$suggestion" "$CB_VAULT_ID"; then
		suggestion=""
	fi
	while true; do
		ask CB_VAULT_NAME "Name of the vault (its folder in $CB_MOUNT_DIR)" "$suggestion"
		if ! valid_vault_name "$CB_VAULT_NAME"; then
			warn "Please choose another name: at most $VAULT_NAME_MAX characters, no dot or space at the start, none of these: $VAULT_NAME_FORBIDDEN"
		elif vault_name_taken "$CB_VAULT_NAME" "$CB_VAULT_ID"; then
			warn "There is a vault called '$CB_VAULT_NAME' already."
		# Up to CryptBridge 0.8 the folder itself was the vault (mounted):
		# what is in there then belongs to the vault and is no obstacle.
		elif [[ "$CB_VAULT_NAME" != "$own" ]] && is_not_mounted "$CB_MOUNT_DIR" && [[ -e "$CB_MOUNT_DIR/$CB_VAULT_NAME" ]]; then
			warn "$CB_MOUNT_DIR/$CB_VAULT_NAME exists already. Please choose another name."
		else
			return 0
		fi
		suggestion=""
	done
}

# find_vault — prints the number of the vault on this server that the keys
# just read belong to, if there is one.
find_vault() {
	local wanted id
	wanted="$CB_SERVICE|$(normalise_path "$CB_VAULT_PATH")|$CB_FINGERPRINT"
	for id in $(vault_ids); do
		if [[ "$(vault_identity "$id")" == "$wanted" ]]; then
			echo "$id"
			return 0
		fi
	done
}

# choose_vaults — the first setup adds a vault. A later one keeps the vaults,
# checks that their clouds still accept the sign-in, and offers to add a
# vault or to renew one (with a new transfer block).
choose_vaults() {
	step "Vaults"
	local ids id free
	ids="$(vault_ids)"
	if [[ -z "$ids" ]]; then
		add_vault
		return
	fi
	for id in $ids; do
		select_vault "$id"
		if [[ "$CB_VAULT_STATE" == "off" ]]; then
			info "$CB_VAULT_NAME ($(service_title)): switched off"
		elif free="$(reach_cloud)"; then
			ok "$CB_VAULT_NAME: $(service_title) reached${free:+: free $free}"
		else
			warn "$CB_VAULT_NAME: $free"
		fi
	done
	if confirm "Add a vault, or renew one with a new transfer block?" n; then
		add_vault
	fi
}

# add_vault — reads the keys of one vault, checks them and adds the vault. If
# the vault is on this server already, it is renewed instead: it takes the
# new sign-in (after signing in again in the Windows program, say). The
# keys wait in NEW_RCLONE_CONF until they are checked, so a wrong block
# changes no vault. The caller writes the service and starts it.
add_vault() {
	get_keys
	RCLONE_CONF="$NEW_RCLONE_CONF"
	configure_remotes
	verify_keys
	compute_fingerprint
	local id
	id="$(find_vault)"
	if [[ -n "$id" ]]; then
		renew_vault "$id"
		return
	fi
	CB_VAULT_ID="$(next_vault_id)"
	choose_vault_name "$(suggested_vault_name)"
	CB_VAULT_STATE="on"
	RCLONE_CONF="$RCLONE_DIR/$CB_VAULT_ID.conf"
	as_user mv -f "$NEW_RCLONE_CONF" "$RCLONE_CONF"
	save_vault
	ok "Vault '$CB_VAULT_NAME' added: $(vault_dir)"
}

# renew_vault ID — vault ID takes the keys just read. Its mount stops first:
# still running, it would write its old sign-in back over the new one.
renew_vault() {
	select_vault "$1"
	systemctl stop "$(vault_unit)" >>"$LOG_FILE" 2>&1 || true
	as_user mv -f "$NEW_RCLONE_CONF" "$RCLONE_CONF"
	ok "Vault '$CB_VAULT_NAME' is on this server already: it takes the new sign-in and keys"
}

# pending_uploads ID — how many files of vault ID wait in its cache for their
# upload. rclone marks them "Dirty" in its notes on the cache, and the notes
# stay while the vault is off (measured with rclone 1.75).
pending_uploads() {
	local notes="$CACHE_DIR/$1/vfsMeta"
	if [[ ! -d "$notes" ]]; then
		echo 0
		return
	fi
	{ grep -rlE '"Dirty": *true' "$notes" 2>/dev/null || true; } | wc -l
}

# remove_vault_dir — removes the folder of the selected vault, which is empty
# once the vault is unmounted. What was put in there by hand stays.
remove_vault_dir() {
	local dir
	dir="$(vault_dir)"
	[[ -d "$dir" ]] || return 0
	as_user rmdir "$dir" 2>/dev/null || warn "$dir is not empty, so it stays. The files in there are NOT in the vault."
}

# select_vault_named NAME — selects the vault called NAME (upper and lower
# case do not matter), or stops and names the vaults there are.
select_vault_named() {
	local wanted="${1,,}" id name
	[[ -n "$wanted" ]] || die "Which vault? Vaults: $(vault_names). For example:  sudo cryptbridge vault off NAME"
	for id in $(vault_ids); do
		name="$(vault_name "$id")"
		if [[ "${name,,}" == "$wanted" ]]; then
			select_vault "$id"
			return 0
		fi
	done
	die "There is no vault called '$1' on this server. Vaults: $(vault_names)"
}

# require_vaults — the vault commands need a server that the setup has set
# up, and has updated from the one vault of CryptBridge 0.8.
require_vaults() {
	[[ -r "$SETTINGS_FILE" ]] || die "CryptBridge is not set up yet. Run:  sudo bash setup.sh"
	if [[ -s "$SINGLE_RCLONE_CONF" || -f "$OLD_RCLONE_CONF" ]]; then
		die "Please run the current setup once first; it updates this server to several vaults:  sudo bash setup.sh"
	fi
	load_settings
	load_user
}

# list_vaults — "cryptbridge vault": the vaults and their state.
list_vaults() {
	load_settings
	show_vaults
}

# vault_add — "cryptbridge vault add": adds a vault, or renews one, without
# running the whole setup and without touching the other vaults.
vault_add() {
	require_root vault add
	require_vaults
	add_vault
	write_vault_unit
	systemctl daemon-reload
	if [[ "$CB_VAULT_STATE" == "on" ]]; then
		start_vault || exit 1
		ok "Video players find it as the folder '$CB_VAULT_NAME' in the share."
	else
		info "The vault is switched off. Switch it on with:  sudo cryptbridge vault on \"$CB_VAULT_NAME\""
	fi
}

# switch_vault on|off NAME — a switched-off vault is unmounted, and its folder
# removed, so that video players and the file manager no longer show it.
# Files waiting for their upload stay in its cache and go up once it is on
# again. The choice lasts through a reboot and a new setup run.
switch_vault() {
	local wanted="$1" name="$2" pending
	require_root vault "$wanted" "$name"
	require_vaults
	select_vault_named "$name"
	if [[ "$wanted" == "off" ]]; then
		# What counts is that the vault is unmounted afterwards; a missing
		# service file (a setup ended early) is no reason to stop.
		systemctl disable --now "$(vault_unit)" >>"$LOG_FILE" 2>&1 || true
		is_not_mounted "$(vault_dir)" || die "Could not stop the vault. See: journalctl -u $(vault_unit)"
		remove_vault_dir
		CB_VAULT_STATE="off"
		save_vault
		ok "Vault '$CB_VAULT_NAME' switched off"
		pending="$(pending_uploads "$CB_VAULT_ID")"
		if ((pending > 0)); then
			info "$pending file(s) still wait for their upload. They go up once the vault is on again."
		fi
		return 0
	fi
	if [[ "$(vault_state)" == "running" ]]; then
		ok "Vault '$CB_VAULT_NAME' is on already"
		return 0
	fi
	CB_VAULT_STATE="on"
	save_vault
	start_vault || exit 1
	ok "Vault '$CB_VAULT_NAME' switched on: $(vault_dir)"
}

# delete_vault NAME — removes a vault from this server only: its service,
# folder, cache and keys. The files in the cloud are not touched. Files that
# still wait for their upload would be lost: they are counted, and the vault
# goes only after an explicit yes.
delete_vault() {
	local name="$1" pending
	require_root vault delete "$name"
	require_vaults
	select_vault_named "$name"
	step "Delete the vault '$CB_VAULT_NAME' from this server"
	info "Your files in $(service_title) are NOT touched. Deleted here: the keys, the cache and the folder."
	pending="$(pending_uploads "$CB_VAULT_ID")"
	if ((pending > 0)); then
		warn "$pending file(s) of this vault are not uploaded yet. Deleting the vault now loses them."
		info "Better: switch the vault on (if it is off), wait until they are uploaded, then delete it."
	fi
	if ! confirm "Delete the vault '$CB_VAULT_NAME' from this server?" n; then
		info "Nothing deleted."
		return 0
	fi
	# Guards the "rm -rf" below.
	[[ "$CB_VAULT_ID" =~ ^[0-9]+$ ]] || die "Unexpected vault number '$CB_VAULT_ID'."
	systemctl disable --now "$(vault_unit)" >>"$LOG_FILE" 2>&1 || true
	rm -f "$UNIT_DIR/$(vault_unit)"
	systemctl daemon-reload
	remove_vault_dir
	rm -rf "${CACHE_DIR:?}/$CB_VAULT_ID"
	rm -f "$RCLONE_DIR/$CB_VAULT_ID.conf" "$VAULTS_DIR/$CB_VAULT_ID"
	ok "Vault '$CB_VAULT_NAME' deleted from this server"
}

# ==========================================================================
# Update from CryptBridge 0.8: one vault, mounted at CB_MOUNT_DIR itself
# ==========================================================================

# update_single_vault — up to CryptBridge 0.8 the server held one vault: one
# rclone configuration, the cache directly in CACHE_DIR, and the vault
# mounted at CB_MOUNT_DIR itself. It becomes vault 1, in a folder of its own
# in CB_MOUNT_DIR; files waiting for their upload move along with the cache.
# The vault runs again at the end of this step, whatever the rest of the
# setup does. The 0.8 configuration goes last: a setup run again after an
# interruption takes the update up again.
update_single_vault() {
	copy_old_rclone_conf # CryptBridge 0.7 kept it elsewhere
	[[ -s "$SINGLE_RCLONE_CONF" ]] || return 0
	step "Update: several vaults on this server"
	RCLONE_CONF="$SINGLE_RCLONE_CONF"
	read_kept_keys
	if [[ -z "${CB_FINGERPRINT:-}" ]]; then
		EXPECTED_FINGERPRINT=""
		compute_fingerprint
	fi
	# Normally vault 1. A setup run again after an interruption finds the
	# vault it began to update, and keeps its name as the suggestion.
	CB_VAULT_ID="$(find_vault)"
	CB_VAULT_NAME=""
	if [[ -n "$CB_VAULT_ID" ]]; then
		CB_VAULT_NAME="$(vault_name "$CB_VAULT_ID")"
	else
		CB_VAULT_ID="$(next_vault_id)"
	fi
	CB_VAULT_STATE="on"
	info "From now on, every vault has a folder of its own in $CB_MOUNT_DIR, and video players"
	info "see each vault as a folder of the same share. Your vault in $(service_title) moves into one."
	choose_vault_name "$(suggested_vault_name)"

	# The cache may only move while rclone is off.
	systemctl disable --now rclone-webdav.service rclone-mount.service >>"$LOG_FILE" 2>&1 || true
	wait_for "the vault to be unmounted" 30 is_not_mounted "$CB_MOUNT_DIR"
	move_single_cache
	write_private "$RCLONE_DIR/$CB_VAULT_ID.conf" <"$SINGLE_RCLONE_CONF"
	RCLONE_CONF="$RCLONE_DIR/$CB_VAULT_ID.conf"
	save_vault
	rm -f "$OLD_MOUNT_UNIT"
	install -d -m 0755 -o "$CB_USER" -g "$CB_GROUP" "$CB_MOUNT_DIR"
	write_webdav_unit
	write_vault_unit
	systemctl daemon-reload
	start_vault || true # explained already; the setup goes on
	systemctl enable --now rclone-webdav.service >>"$LOG_FILE" 2>&1 || warn "WebDAV did not start. See: journalctl -u rclone-webdav"
	rm -f "$SINGLE_RCLONE_CONF"
	remove_old_rclone_conf
	save_settings
	ok "Your vault is now '$CB_VAULT_NAME': $(vault_dir)"
}

# move_single_cache — the cache of the one vault into the folder of vault 1.
# The vault's remote keeps its name (cbvault), so rclone finds its files
# waiting for upload there. Moved as the desktop user, who owns the cache.
move_single_cache() {
	local part target="$CACHE_DIR/$CB_VAULT_ID"
	as_user mkdir -p "$target"
	for part in vfs vfsMeta; do
		[[ -d "$CACHE_DIR/$part" ]] || continue
		if [[ -e "$target/$part" ]]; then
			warn "$CACHE_DIR/$part was not moved: $target/$part exists already."
			continue
		fi
		as_user mv "$CACHE_DIR/$part" "$target/$part"
	done
}

# ==========================================================================
# Cache, WebDAV account, address
# ==========================================================================

choose_cache() {
	step "Cache on this server"
	install -d -m 0750 -o "$CB_USER" -g "$CB_GROUP" "$CACHE_DIR"
	local free_gb vaults default_max
	free_gb="$(df -BG --output=avail "$CACHE_DIR" | tail -n1 | tr -dc '0-9')"
	((free_gb / 2 >= 2)) || die "Only ${free_gb} GB free on the disk — not enough for the cache."
	# Each vault caches up to the limit; half of the free space is shared out.
	# Adding a vault later leaves the limit alone (it would restart all vaults):
	# the space kept free below protects the disk in any case.
	vaults="$(vault_ids | wc -l)"
	((vaults > 0)) || vaults=1
	default_max=$((free_gb / 2 / vaults))
	((default_max > DEFAULT_CACHE_MAX_GB)) && default_max=$DEFAULT_CACHE_MAX_GB
	((default_max < 1)) && default_max=1
	info "Free disk space: ${free_gb} GB. Films are cached while they play; the cache is cleaned automatically."
	info "Every vault has a cache of its own. Files you copy into a vault folder are kept here until they are uploaded."
	ask CB_CACHE_MAX "Cache size per vault (soft limit)" "${CB_CACHE_MAX:-${default_max}G}"
	ask CB_CACHE_MIN_FREE "Always keep this much disk space free (all vaults together)" "${CB_CACHE_MIN_FREE:-5G}"
	[[ "$CB_CACHE_MAX" =~ ^[1-9][0-9]*[MGT]$ && "$CB_CACHE_MIN_FREE" =~ ^[1-9][0-9]*[MGT]$ ]] ||
		die "Sizes must look like 20G or 500M."
}

choose_webdav_account() {
	step "Account for streaming"
	if [[ -s "$HTPASSWD" ]] && [[ -n "${CB_WEBDAV_USER:-}" ]] && confirm "Keep the streaming account '$CB_WEBDAV_USER'?" y; then
		return
	fi
	ask CB_WEBDAV_USER "User name for streaming" "${CB_WEBDAV_USER:-stream}"
	[[ "$CB_WEBDAV_USER" =~ ^[A-Za-z0-9._-]+$ ]] || die "Use letters, digits, dot, dash or underscore only."
	local pw again
	while true; do
		ask_secret pw "Password for streaming (at least 12 characters)"
		((${#pw} >= 12)) || {
			warn "Too short."
			continue
		}
		ask_secret again "Password again"
		[[ "$pw" == "$again" ]] && break
		warn "The passwords differ."
	done
	# bcrypt with cost 10, password passed on stdin (not in the process list).
	# rclone checks the password on EVERY request; with cost 14 (Caddy's
	# fixed default) each request took 1.1-1.7 s (measured), which makes
	# seeking and browsing in the player sluggish. Cost 10 is ~16 times faster
	# and, with a long password and fail2ban, still far out of reach.
	local line
	line="$(printf '%s\n' "$pw" | htpasswd -niBC "$BCRYPT_COST" "$CB_WEBDAV_USER" 2>/dev/null)" || die "Could not hash the password."
	[[ "$line" == "$CB_WEBDAV_USER:\$2"* ]] || die "Unexpected password hash."
	install -m 0640 -o root -g "$CB_GROUP" /dev/null "$HTPASSWD"
	printf '%s\n' "$line" >"$HTPASSWD"
	WEBDAV_PASSWORD_FOR_TEST="$pw"
	ok "Streaming account '$CB_WEBDAV_USER' saved"
}

# Filled by check_web_ports and choose_address, read by the later steps.
HTTPS_POSSIBLE=1 # 0: another program uses port 80 or 443, so no Caddy
ADDRESS_IS_IP=0  # 1: the user kept an IP address, which gets no trusted certificate

# How long check_https waits for the certificate; the step title says so.
readonly HTTPS_WAIT_SECONDS=180
readonly HTTPS_WAIT_TEXT="up to 3 minutes"
# With an IP address Caddy serves its own certificate at once; no waiting.
readonly HTTPS_WAIT_SECONDS_IP=20

# web_port_users — the programs besides Caddy that listen on port 80 or 443,
# one line each: "PROGRAM UNIT PORTS", e.g. "nginx nginx.service 80 and 443".
# Caddy is left out: on a repeated setup it holds the ports itself.
web_port_users() {
	command -v ss >/dev/null || return 0
	ss -Htlnp '( sport = :80 or sport = :443 )' 2>/dev/null |
		while read -r _ _ _ local_address _ process; do
			local port="${local_address##*:}" pid program unit
			for pid in $(grep -o 'pid=[0-9]*' <<<"$process" | cut -d= -f2 || true); do
				program="$(ps -o comm= -p "$pid" 2>/dev/null || true)"
				unit="$(ps -o unit= -p "$pid" 2>/dev/null || true)"
				# "-" keeps the three fields apart when ps knows no name or unit.
				printf '%s %s %s\n' "$port" "${program:--}" "${unit:--}"
			done
		done |
		awk '$2 != "caddy"' | sort -u | sort -n |
		awk '{ key = $2 " " $3; ports[key] = (key in ports) ? ports[key] " and " $1 : $1 }
			END { for (key in ports) print key, ports[key] }'
}

# warn_port_user PROGRAM PORTS — one line of web_port_users in words, e.g.
# "Ports 80 and 443 are used by nginx."
warn_port_user() {
	if [[ "$2" == *" and "* ]]; then
		warn "Ports $2 are used by $1."
	else
		warn "Port $2 is used by $1."
	fi
}

# Paused web servers: another web server on ports 80 and 443 (a website, say)
# can be paused for as long as Caddy holds the ports for streaming. Whenever
# Caddy lets go of them again (streaming switched off, CryptBridge removed,
# Caddy not starting), the paused web servers get back the state they had.
# Only web servers that CryptBridge paused itself are ever started.

# is_paused_web_server UNIT — whether UNIT is noted as paused
is_paused_web_server() {
	[[ -r "$PAUSED_WEB_SERVERS" ]] &&
		awk -v unit="$1" '$1 == unit { found = 1 } END { exit !found }' "$PAUSED_WEB_SERVERS"
}

# pause_web_server UNIT — stops UNIT and notes it, with whether it starts
# with the server. Paused, it must not start at boot either: it would take
# the ports from Caddy. It is noted before it is stopped, so that a setup
# that ends right afterwards cannot forget it; a web server paused already
# keeps its first note, which has the state from before CryptBridge.
pause_web_server() {
	local unit="$1" start_at_boot=disabled
	if [[ "$(systemctl is-enabled "$unit" 2>/dev/null || true)" == enabled ]]; then
		start_at_boot=enabled
	fi
	if ! is_paused_web_server "$unit"; then
		[[ -d "$CONF_DIR" ]] || install -d -m 0750 "$CONF_DIR" # first setup run
		printf '%s %s\n' "$unit" "$start_at_boot" >>"$PAUSED_WEB_SERVERS"
	fi
	if [[ "$start_at_boot" == enabled ]]; then
		systemctl disable --now "$unit" >>"$LOG_FILE" 2>&1 || die "Could not stop $unit. See: journalctl -u $unit"
	else
		systemctl stop "$unit" >>"$LOG_FILE" 2>&1 || die "Could not stop $unit. See: journalctl -u $unit"
	fi
}

# offer_to_pause_web_servers — names every other program on port 80 or 443
# and offers to pause the web servers among them. Returns 0 when the ports
# are free afterwards.
offer_to_pause_web_servers() {
	local users program unit ports
	users="$(web_port_users)"
	[[ -n "$users" ]] || return 0
	while read -r program unit ports; do
		warn_port_user "$program" "$ports"
		if [[ "$unit" == *.service ]] &&
			confirm "Pause $program while streaming is on? Switching streaming off offers to start it again." y; then
			pause_web_server "$unit"
			ok "$program paused while streaming is on"
		fi
	done <<<"$users"
	[[ -z "$(web_port_users)" ]]
}

# resume_web_servers ask|now — gives every paused web server back the state
# it had, once Caddy has let go of ports 80 and 443: "ask" first when the
# user switches streaming off or removes CryptBridge, "now" when Caddy cannot
# have the ports after all. Afterwards they are forgotten; after a "no" the
# user gets the command to start one later.
resume_web_servers() {
	local mode="$1" lines line unit start_at_boot program
	local -a start_command
	[[ -s "$PAUSED_WEB_SERVERS" ]] || return 0
	mapfile -t lines <"$PAUSED_WEB_SERVERS"
	for line in "${lines[@]}"; do
		read -r unit start_at_boot <<<"$line"
		# A unit name starting with "-" would reach systemctl as an option.
		if [[ ! "$unit" =~ ^[[:alnum:]][[:alnum:]@._:-]*\.service$ || ! "$start_at_boot" =~ ^(enabled|disabled)$ ]]; then
			warn "Skipped an unreadable line in $PAUSED_WEB_SERVERS: $line"
			continue
		fi
		program="${unit%.service}"
		start_command=(start)
		[[ "$start_at_boot" == enabled ]] && start_command=(enable --now)
		if [[ "$(systemctl show "$unit" -p LoadState --value 2>/dev/null || true)" == not-found ]]; then
			info "$program was paused for streaming, but it is not installed any more."
			continue
		fi
		if [[ "$mode" == ask ]] && ! confirm "Start $program again (paused for streaming)?" y; then
			info "$program stays off. Start it later with:  sudo systemctl ${start_command[*]} $unit"
			continue
		fi
		if systemctl "${start_command[@]}" "$unit" >>"$LOG_FILE" 2>&1; then
			ok "$program started again"
		else
			warn "$program did not start. See: journalctl -u $unit"
		fi
	done
	rm -f "$PAUSED_WEB_SERVERS"
}

# give_ports_back — Caddy did not start after all: the paused web servers get
# ports 80 and 443 back at once, and Caddy must not take them at the next boot.
give_ports_back() {
	[[ -s "$PAUSED_WEB_SERVERS" ]] || return 0
	systemctl disable --now caddy >>"$LOG_FILE" 2>&1 || true
	resume_web_servers now
}

# check_web_ports — HTTPS needs ports 80 and 443 for Caddy. Another web
# server there (a website, say) used to stop the setup halfway, before the
# command and the desktop icons; now it is named at the start and the user
# decides. Without the ports, everything else is still set up.
check_web_ports() {
	step "Ports for HTTPS"
	if [[ "$CB_STREAMING" == "off" ]]; then
		info "Streaming is switched off, so ports 80 and 443 stay with other programs (a website, say)."
		return
	fi
	if offer_to_pause_web_servers; then
		ok "Ports 80 and 443 are free for HTTPS"
		return
	fi
	# Pausing a web server is no use while another program keeps the ports.
	resume_web_servers now
	HTTPS_POSSIBLE=0
	warn "The setup goes on without HTTPS: the vault folder, the 'cryptbridge' command and the desktop work,"
	warn "video players cannot connect yet. Free the ports later, then run:  sudo cryptbridge setup"
}

public_ipv4() {
	ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1)}'
}

# public_dns_ipv4 HOST — the IPv4 addresses of HOST in the PUBLIC DNS, which
# is what Let's Encrypt sees. Asking the server itself is no good: Debian
# and Ubuntu map the server's own name to 127.0.1.1 in /etc/hosts, and the
# provider's resolver may fail (measured on a Contabo VPS: SERVFAIL).
public_dns_ipv4() {
	local json
	json="$(curl -fsS --max-time 10 -H 'accept: application/dns-json' \
		"https://1.1.1.1/dns-query?name=$1&type=A" 2>/dev/null)" || return 1
	jq -r '.Answer[]? | select(.type == 1) | .data' <<<"$json"
}

# is_ipv4_address TEXT — true for an address such as 157.173.96.238.
is_ipv4_address() {
	[[ "$1" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]]
}

choose_address() {
	step "Address of this server"
	info "Video players reach this server over HTTPS. A free certificate is fetched automatically;"
	info "that needs a name that points to this server: your own domain, or a free name"
	info "from duckdns.org (e.g. myvault.duckdns.org — set its IP to this server's IP)."
	info "Host names of your provider (…supersrv.de, …contaboserver.net) often do NOT work:"
	info "all customers share one Let's Encrypt limit of 50 certificates per week."
	local resolved mine
	mine="$(public_ipv4 || true)"
	while true; do
		ask CB_ADDRESS "Domain or host name of this server" "${CB_ADDRESS:-}"
		CB_ADDRESS="${CB_ADDRESS,,}"
		is_ipv4_address "$CB_ADDRESS" || break
		warn "An IP address gets no trusted certificate, so video players refuse the connection."
		info "Please enter a name instead, e.g. a free one from duckdns.org pointing to ${mine:-this server}."
		if confirm "Continue with the IP address anyway?" n; then
			ADDRESS_IS_IP=1
			info "Caddy serves HTTPS with a certificate of its own, which only players that ignore certificates accept."
			return
		fi
		CB_ADDRESS="" # no IP as the suggestion of the next question
	done
	[[ "$CB_ADDRESS" =~ ^[a-z0-9.-]+$ && "$CB_ADDRESS" == *.* ]] || die "Please enter a host name such as myvault.duckdns.org."
	# "|| true": an unanswered query is handled below, not by set -e.
	resolved="$(public_dns_ipv4 "$CB_ADDRESS" || true)"
	if [[ -z "$resolved" ]]; then
		info "Could not look up '$CB_ADDRESS' in the public DNS; the HTTPS check at the end shows whether it works."
	elif [[ -n "$mine" && $'\n'"$resolved"$'\n' != *$'\n'"$mine"$'\n'* ]]; then
		warn "'$CB_ADDRESS' points to $(tr '\n' ' ' <<<"$resolved")— but this server is $mine."
		warn "The certificate will fail until the name points to this server."
		confirm "Continue anyway?" n || exit 1
	else
		ok "$CB_ADDRESS points to this server ($mine)"
	fi
}

# ==========================================================================
# Services
# ==========================================================================

# write_units — the WebDAV service and the service of every vault. The
# folder of the vaults itself is never mounted, so root may create it even
# while the vaults in it are mounted.
write_units() {
	step "Services"
	install -d -m 0755 -o "$CB_USER" -g "$CB_GROUP" "$CB_MOUNT_DIR"
	write_webdav_unit
	local id
	for id in $(vault_ids); do
		select_vault "$id"
		write_vault_unit
	done
	systemctl daemon-reload
	ok "Services written: rclone-webdav.service and one ${VAULT_UNIT_PREFIX}NUMBER.service per vault"
}

# dir_cache_time — how long the mount of the selected vault keeps folder
# listings; see QUIET_DIR_CACHE_TIME.
dir_cache_time() {
	if [[ "$CB_SERVICE" == "mega" ]]; then
		printf '%s' "$QUIET_DIR_CACHE_TIME"
	else
		printf '%s' "$DIR_CACHE_TIME"
	fi
}

# write_vault_unit — the mount service of the selected vault. Its folder is
# written in quotes, as the name may hold spaces; valid_vault_name keeps out
# the characters that systemd reads differently in quotes.
write_vault_unit() {
	local dir
	dir="$(vault_dir)"
	cat >"$UNIT_DIR/$(vault_unit)" <<EOF
$MARKER
[Unit]
Description=CryptBridge: vault "$CB_VAULT_NAME" in $(service_title)
Documentation=https://github.com/burnersen/CryptBridge
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=0

[Service]
Type=notify
User=$CB_USER
Group=$CB_GROUP
# A crash leaves a dead mount behind ("Transport endpoint is not connected");
# clear it first. "-": usually there is nothing to clear.
ExecStartPre=-/usr/bin/fusermount3 -uz "$dir"
# A switched-off vault has no folder, so that players do not show it.
ExecStartPre=/usr/bin/mkdir -p "$dir"
# The check file stays in the vault for the key check, but neither the folder
# nor WebDAV (which serves the folder) shows it: people took it for clutter.
ExecStart=$RCLONE_BIN mount $VAULT_REMOTE: "$dir" \\
  --exclude /$CHECK_FILE \\
  --config $RCLONE_CONF \\
  --cache-dir $CACHE_DIR/$CB_VAULT_ID \\
  --vfs-cache-mode $CACHE_MODE \\
  --vfs-cache-max-size $CB_CACHE_MAX \\
  --vfs-cache-min-free-space $CB_CACHE_MIN_FREE \\
  --buffer-size $BUFFER_SIZE \\
  --vfs-read-chunk-size $CHUNK_SIZE \\
  --vfs-read-chunk-size-limit $CHUNK_SIZE_LIMIT \\
  --vfs-read-ahead $READ_AHEAD \\
  --dir-cache-time $(dir_cache_time) \\
  --poll-interval $POLL_INTERVAL \\
  --umask 022 \\
  --log-level INFO
ExecStop=/usr/bin/fusermount3 -uz "$dir"
Restart=on-failure
RestartSec=10
TimeoutStopSec=60
# No PrivateTmp/ProtectSystem/ProtectHome here: they give the service its own
# mount namespace, and the vault would be invisible in the desktop.
# No NoNewPrivileges either: it would block the setuid helper fusermount3.

[Install]
WantedBy=multi-user.target
EOF
	chmod 0644 "$UNIT_DIR/$(vault_unit)"
}

# write_webdav_unit — the WebDAV server for the video players. It serves the
# folder of the vaults, so every vault is a folder of the one share. A
# switched-off vault has no folder there, and a vault whose mount failed is
# left out of the list (measured): the other vaults stay available.
write_webdav_unit() {
	cat >"$WEBDAV_UNIT" <<EOF
$MARKER
[Unit]
Description=CryptBridge: WebDAV for video players (read-only), one folder per vault
Documentation=https://github.com/burnersen/CryptBridge
StartLimitIntervalSec=0

[Service]
Type=simple
User=$CB_USER
Group=$CB_GROUP
# Serves the mounted folders: one cache, and new files are visible at once.
# The mounts already cache, so no second cache here. A plain folder needs no
# rclone configuration ("": none, not even one of the user's own).
ExecStart=$RCLONE_BIN serve webdav "$CB_MOUNT_DIR" \\
  --config "" \\
  --addr $WEBDAV_ADDR \\
  --htpasswd $HTPASSWD \\
  --read-only \\
  --vfs-cache-mode off \\
  --dir-cache-time 10s \\
  --no-modtime \\
  --log-level INFO
Restart=always
RestartSec=5
NoNewPrivileges=yes
CapabilityBoundingSet=
RestrictSUIDSGID=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
EOF
	chmod 0644 "$WEBDAV_UNIT"
}

write_caddyfile() {
	if [[ -f "$CADDYFILE" ]] && ! grep -q "^$MARKER" "$CADDYFILE"; then
		cp -a "$CADDYFILE" "$CADDYFILE.before-cryptbridge"
		info "The previous Caddyfile was saved as $CADDYFILE.before-cryptbridge"
	fi
	cat >"$CADDYFILE" <<EOF
$MARKER — rewritten by the CryptBridge setup.
$CB_ADDRESS {
	reverse_proxy $WEBDAV_ADDR {
		# Send video data on immediately instead of buffering it.
		flush_interval -1
	}
	# Access log into the systemd journal, where fail2ban reads it. A file
	# under /var/log does not work: Ubuntu's /var/log is root:syslog 0750,
	# so the caddy user cannot even reach it (measured on Ubuntu 26.04).
	log {
		output stderr
		format json
	}
}
EOF
	chmod 0644 "$CADDYFILE" # Caddy runs as its own user and must read it
	caddy validate --config "$CADDYFILE" --adapter caddyfile >>"$LOG_FILE" 2>&1 || die "Caddy rejected its configuration. Details: $LOG_FILE"
}

start_services() {
	step "Starting"
	# restart, not "enable --now": on a repeated setup the services already
	# run and must pick up the new settings. Unfinished uploads stay in the
	# cache and continue after the restart.
	local id
	for id in $(vault_ids); do
		select_vault "$id"
		if [[ "$CB_VAULT_STATE" == "on" ]]; then
			start_vault || true # explained already; the other vaults still start
		fi
	done
	systemctl enable rclone-webdav.service >>"$LOG_FILE" 2>&1
	systemctl restart rclone-webdav.service >>"$LOG_FILE" 2>&1 || die "WebDAV did not start. See: journalctl -u rclone-webdav"
	wait_for "WebDAV answering" 30 webdav_answers
	if [[ "$CB_STREAMING" == "off" ]]; then
		# Ready for "streaming on" with the new settings, but not started:
		# the ports may belong to another web server now.
		write_caddyfile
		systemctl disable --now caddy >>"$LOG_FILE" 2>&1 || warn "Could not stop Caddy. See: journalctl -u caddy"
		info "HTTPS (Caddy) prepared but not started: streaming is switched off."
		return
	fi
	if ((HTTPS_POSSIBLE == 0)); then
		info "HTTPS (Caddy) skipped: port 80 or 443 is in use by another program (see the start of the setup)."
		return
	fi
	write_caddyfile
	systemctl enable caddy >>"$LOG_FILE" 2>&1
	if ! systemctl reload-or-restart caddy >>"$LOG_FILE" 2>&1; then
		# Not fatal: the vault works without HTTPS, and the steps after this
		# one (command, desktop) must still run.
		HTTPS_POSSIBLE=0
		warn "Caddy did not start, so there is no HTTPS for now. See: journalctl -u caddy"
		give_ports_back
		return
	fi
	ok "Caddy configured for https://$CB_ADDRESS"
}

# start_vault — (re)starts the selected vault and waits for its folder. If it
# does not come up, says so and fails, but does not stop the setup: one vault
# must not keep the others from starting.
start_vault() {
	local unit dir
	unit="$(vault_unit)"
	dir="$(vault_dir)"
	systemctl enable "$unit" >>"$LOG_FILE" 2>&1
	if systemctl restart "$unit" >>"$LOG_FILE" 2>&1 &&
		wait_until "vault '$CB_VAULT_NAME' mounted at $dir" 60 is_mounted "$dir"; then
		return 0
	fi
	warn "Vault '$CB_VAULT_NAME' did not start. See: journalctl -u ${unit%.service}"
	return 1
}

# is_mounted DIR asks the kernel's mount table instead of looking into the
# folder: a FUSE mount of the desktop user refuses everybody else — even
# root — so "mountpoint" run as root would wrongly say "not mounted".
is_mounted() {
	findmnt -rn --mountpoint "$1" >/dev/null 2>&1
}

is_not_mounted() {
	! is_mounted "$1"
}

webdav_answers() {
	local code
	code="$(curl -s -o /dev/null -w '%{http_code}' "http://$WEBDAV_ADDR/")" || return 1
	[[ "$code" == "401" ]] # answering, and refusing without password
}

# wait_until "what" SECONDS command... — waits until command succeeds and
# shows progress instead of hanging silently; fails after SECONDS.
wait_until() {
	local what="$1" seconds="$2" i
	shift 2
	printf '    waiting for %s ' "$what"
	for ((i = 0; i < seconds; i++)); do
		if "$@" >/dev/null 2>&1; then
			printf '\n'
			ok "$what"
			return 0
		fi
		printf '.'
		sleep 1
	done
	printf '\n'
	return 1
}

# wait_for "what" SECONDS command... — wait_until, but a timeout stops the setup.
wait_for() {
	wait_until "$@" || die "Timed out waiting for: $1"
}

# explain_certificate_problem reads Caddy's log and says in plain words why
# no certificate came, instead of leaving the user with "see the log".
explain_certificate_problem() {
	local log
	log="$(journalctl -u caddy --since "-15min" --no-pager -o cat 2>/dev/null || true)"
	if [[ "$log" == *"rateLimited"* ]]; then
		local domain
		domain="$(grep -o 'already issued for \\"[^\\]*' <<<"$log" | tail -n1 | sed 's/.*\\"//' || true)"
		warn "Let's Encrypt refuses: its limit of 50 certificates per week for '${domain:-this domain}' is used up."
		warn "That happens with provider host names — all their customers share the limit."
		warn "Fix: get a free name at duckdns.org pointing to this server, then run:  sudo cryptbridge setup"
	elif [[ "$log" == *"Timeout during connect"* || "$log" == *"connection refused"* || "$log" == *"Connection refused"* ]]; then
		warn "Let's Encrypt could not reach this server. Open ports 80 and 443 — also in your provider's firewall."
	else
		warn "Check that ports 80 and 443 are open (also in the provider's firewall). Details: journalctl -u caddy"
	fi
	info "Caddy keeps trying by itself; once it gets a certificate, your video player works without running the setup again."
}

check_https() {
	if [[ "$CB_STREAMING" == "off" ]]; then
		step "Checking HTTPS"
		info "Skipped: streaming is switched off."
		return
	fi
	if ((HTTPS_POSSIBLE == 0)); then
		step "Checking HTTPS"
		warn "Skipped: there is no HTTPS yet. Once ports 80 and 443 are free, run:  sudo cryptbridge setup"
		return
	fi
	local wait_seconds="$HTTPS_WAIT_SECONDS" curl_options=()
	if ((ADDRESS_IS_IP)); then
		step "Checking HTTPS"
		# Nobody trusts Caddy's own certificate, curl neither; it is there at
		# once, so a long wait would only look like a hang.
		wait_seconds="$HTTPS_WAIT_SECONDS_IP"
		curl_options=(--insecure)
	else
		step "Checking HTTPS ($HTTPS_WAIT_TEXT: the certificate is fetched now)"
	fi
	local deadline=$((SECONDS + wait_seconds)) code=""
	printf '    '
	while ((SECONDS < deadline)); do
		code="$(curl -s "${curl_options[@]}" -o /dev/null -w '%{http_code}' --max-time 5 "https://$CB_ADDRESS/" || true)"
		[[ "$code" == "401" ]] && break
		printf '.'
		sleep 2
	done
	printf '\n'
	if [[ "$code" != "401" ]]; then
		warn "https://$CB_ADDRESS does not answer yet (last answer: ${code:-none})."
		if ((ADDRESS_IS_IP)); then
			warn "Details: journalctl -u caddy"
		else
			explain_certificate_problem
		fi
		return
	fi
	if ((ADDRESS_IS_IP)); then
		ok "https://$CB_ADDRESS answers (with Caddy's own certificate) and asks for the password"
	else
		ok "https://$CB_ADDRESS answers and asks for the password"
	fi
	if [[ -n "${WEBDAV_PASSWORD_FOR_TEST:-}" ]]; then
		code="$(curl -s "${curl_options[@]}" -o /dev/null -w '%{http_code}' --max-time 10 -u "$CB_WEBDAV_USER:$WEBDAV_PASSWORD_FOR_TEST" -X PROPFIND -H 'Depth: 1' "https://$CB_ADDRESS/")"
		[[ "$code" == "207" ]] && ok "Login with the streaming account works" || warn "Login test answered $code"
	fi
}

# ==========================================================================
# Firewall and fail2ban
# ==========================================================================

# Ports through which the admin reaches the server: they must stay open, or
# switching the firewall on locks the admin out.
admin_ports() {
	local ports=() port line
	[[ -n "${CB_SSH_PORT:-}" ]] && ports+=("$CB_SSH_PORT")
	# SSH by socket activation (Ubuntu 22.10 and later): the socket belongs to
	# systemd, not to sshd. One line per address, e.g. "[::]:19079 (Stream)".
	while read -r line; do
		[[ "$line" =~ :([0-9]+)\ \(Stream\) ]] && ports+=("${BASH_REMATCH[1]}")
	done < <(systemctl show ssh.socket -p Listen --value 2>/dev/null || true)
	# Classic sshd, xrdp (remote desktop) and VNC servers, by process name.
	while read -r line; do
		[[ "$line" =~ :([0-9]+)[[:space:]].*\"(sshd|xrdp|Xvnc|x11vnc)\" ]] && ports+=("${BASH_REMATCH[1]}")
	done < <(ss -Htlnp 2>/dev/null || true)
	printf '%s\n' "${ports[@]}" | sort -un
}

setup_firewall() {
	step "Firewall (ufw)"
	local ports port status
	mapfile -t ports < <(admin_ports)
	status="$(ufw status 2>/dev/null | head -n1 || true)"
	info "Ports that will be opened: 80 and 443 (HTTPS for your video player)."
	# Allowed first, whatever happens below: ufw keeps rules while it is off
	# and applies them once it is switched on, also later by hand. Allowing
	# them only after switching it on left them closed when the firewall was
	# left off here and switched on afterwards.
	ufw allow 80/tcp comment "CryptBridge HTTPS certificate" >>"$LOG_FILE"
	ufw allow 443/tcp comment "CryptBridge WebDAV for video players" >>"$LOG_FILE"
	if [[ "$status" == *"active"* && "$status" != *"inactive"* ]]; then
		ok "Firewall active"
		return
	fi
	info "ufw is not active yet. Before switching it on, these ports for your own access stay open:"
	if ((${#ports[@]} == 0)); then
		warn "No SSH or remote desktop port found. Switching on the firewall could lock you out."
		if confirm "Skip the firewall (recommended in this case)?" y; then
			info "Firewall left off. Ports 80 and 443 are allowed already for when it is switched on."
			return
		fi
	fi
	for port in "${ports[@]}"; do info "  $port/tcp"; done
	confirm "Switch on the firewall with these rules?" y || {
		warn "Firewall left off. Ports 80 and 443 are allowed already for when it is switched on."
		return
	}
	for port in "${ports[@]}"; do ufw allow "$port/tcp" comment "admin access (CryptBridge setup)" >>"$LOG_FILE"; done
	ufw --force enable >>"$LOG_FILE"
	ok "Firewall active"
}

setup_fail2ban() {
	step "Protection against password guessing (fail2ban)"
	cat >"$F2B_FILTER" <<'EOF'
# Managed by CryptBridge
# Counts failed logins at the WebDAV server in Caddy's access log (systemd
# journal): answer 401 although the client DID send a password. Requests
# without a password (the usual first request of a WebDAV client) do not
# count, so a video player never bans itself.
[Definition]
failregex = ^.*"remote_ip":"<HOST>".*"Authorization":\[.*"status":401\b
journalmatch = _SYSTEMD_UNIT=caddy.service
EOF
	cat >"$F2B_JAIL" <<EOF
$MARKER
[cryptbridge-webdav]
enabled  = true
port     = http,https
filter   = cryptbridge-webdav
backend  = systemd
maxretry = 8
findtime = 10m
bantime  = 1h
EOF
	systemctl enable fail2ban >>"$LOG_FILE" 2>&1
	systemctl reload-or-restart fail2ban >>"$LOG_FILE" 2>&1 || die "fail2ban did not accept the new rules. Details: journalctl -u fail2ban"
	ok "8 wrong passwords within 10 minutes block an address for 1 hour"
}

# ==========================================================================
# Desktop
# ==========================================================================

setup_desktop() {
	step "Desktop"
	local desktop uid
	desktop="$(runuser -u "$CB_USER" -- xdg-user-dir DESKTOP 2>/dev/null || true)"
	uid="$(id -u "$CB_USER")"
	if [[ -z "$desktop" || "$desktop" == "$CB_HOME" ]]; then
		desktop="$CB_HOME/Desktop"
	fi
	install -d -m 0755 -o "$CB_USER" -g "$CB_GROUP" "$desktop"

	write_launcher "$desktop/cryptbridge-vault.desktop" "CryptBridge Vault" \
		"Your encrypted vaults, one folder each" "xdg-open $CB_MOUNT_DIR" "drive-harddisk-encrypted" false
	write_launcher "$desktop/cryptbridge.desktop" "CryptBridge" \
		"Status, vaults, streaming settings and restart of CryptBridge" "$SELF_INSTALLED menu" "security-high" true

	# XFCE runs a desktop launcher without asking only if it is marked as
	# trusted with the SHA-256 of its content (XFCE 4.18 and later).
	local bus="/run/user/$uid/bus" file
	if [[ -S "$bus" ]] && command -v gio >/dev/null; then
		for file in "$desktop/cryptbridge-vault.desktop" "$desktop/cryptbridge.desktop"; do
			runuser -u "$CB_USER" -- env DBUS_SESSION_BUS_ADDRESS="unix:path=$bus" \
				gio set -t string "$file" metadata::xfce-exe-checksum "$(sha256sum "$file" | cut -c1-64)" >>"$LOG_FILE" 2>&1 || true
		done
	else
		info "Not signed in to the desktop right now: XFCE will ask once whether to trust the new icons."
	fi

	# Bookmark in the file manager's side bar.
	local bookmarks="$CB_HOME/.config/gtk-3.0/bookmarks"
	install -d -m 0755 -o "$CB_USER" -g "$CB_GROUP" "$(dirname "$bookmarks")"
	if ! grep -qs "file://$CB_MOUNT_DIR " "$bookmarks"; then
		echo "file://$CB_MOUNT_DIR CryptBridge" >>"$bookmarks"
		chown "$CB_USER:$CB_GROUP" "$bookmarks"
	fi

	# Thumbnails of films would download part of every film whenever a folder
	# is opened: exclude the vault from the video thumbnailers.
	exclude_from_thumbnails
	ok "Icons on the desktop, bookmark in the file manager"
}

# write_launcher FILE NAME COMMENT EXEC ICON TERMINAL
write_launcher() {
	cat >"$1" <<EOF
[Desktop Entry]
Version=1.0
Type=Application
Name=$2
Comment=$3
Exec=$4
Icon=$5
Terminal=$6
Categories=Utility;
EOF
	chown "$CB_USER:$CB_GROUP" "$1"
	chmod 0755 "$1"
}

exclude_from_thumbnails() {
	local system_rc="/etc/xdg/tumbler/tumbler.rc" user_rc="$CB_HOME/.config/tumbler/tumbler.rc"
	[[ -r "$system_rc" ]] || return 0
	install -d -m 0755 -o "$CB_USER" -g "$CB_GROUP" "$(dirname "$user_rc")"
	[[ -f "$user_rc" ]] || install -m 0644 -o "$CB_USER" -g "$CB_GROUP" "$system_rc" "$user_rc"
	# In the video sections, add the vault folder to "Excludes=" once.
	awk -v dir="$CB_MOUNT_DIR" '
		/^\[/ { video = ($0 == "[FfmpegThumbnailer]" || $0 == "[GstThumbnailer]") }
		video && /^Excludes=/ && index($0, dir) == 0 {
			sub(/^Excludes=/, "Excludes=" dir ";")
		}
		{ print }
	' "$user_rc" >"$user_rc.new"
	mv "$user_rc.new" "$user_rc"
	chown "$CB_USER:$CB_GROUP" "$user_rc"
}

install_self() {
	local self
	self="$(readlink -f "$0")"
	# Running as "cryptbridge setup" already: nothing to copy.
	[[ "$self" == "$SELF_INSTALLED" ]] && return 0
	install -m 0755 "$self" "$SELF_INSTALLED"
}

# ==========================================================================
# Summary, status, menu, uninstall
# ==========================================================================

show_streaming_settings() {
	load_settings
	printf '\n%sStreaming settings%s (for any video player that opens WebDAV shares)\n' "$BOLD" "$RESET"
	printf '    Protocol:  WebDAV (HTTPS)\n'
	printf '    Address:   %s\n' "${CB_ADDRESS:-?}"
	printf '    Port:      443\n'
	printf '    Path:      /   (every vault is a folder there)\n'
	printf '    User:      %s\n' "${CB_WEBDAV_USER:-?}"
	printf '    Password:  the streaming password you chose during setup\n'
	printf '    Infuse (Apple TV / iPhone): Settings -> Shares -> Add share -> WebDAV.\n'
	printf '    Tip for Infuse: Settings -> Playback/Streaming -> Buffer: "Generous".\n'
	if [[ "$CB_STREAMING" == "off" ]]; then
		warn "Streaming is switched off at the moment. Switch it on in the menu (cryptbridge) or with:  sudo cryptbridge streaming on"
	fi
}

show_status() {
	load_settings
	local unit state
	printf '\n%sCryptBridge %s%s\n' "$BOLD" "$CRYPTBRIDGE_VERSION" "$RESET"
	show_vaults
	for unit in rclone-webdav caddy fail2ban; do
		if [[ "$unit" == "caddy" && "$CB_STREAMING" == "off" ]]; then
			info "caddy: switched off (streaming is off, ports 80 and 443 are free)"
			continue
		fi
		state="$(systemctl is-active "$unit" 2>/dev/null || true)"
		if [[ "$state" == "active" ]]; then ok "$unit: running"; else warn "$unit: $state"; fi
	done
	info "Folder of the vaults: ${CB_MOUNT_DIR:-?}"
}

# show_vaults — one line per vault: its name, cloud, state and check code.
show_vaults() {
	local ids id
	ids="$(vault_ids)"
	if [[ -z "$ids" ]]; then
		if [[ -f "$OLD_MOUNT_UNIT" ]]; then
			warn "This server still has the one vault of CryptBridge 0.8. Run the setup once to update it:  sudo bash setup.sh"
		else
			warn "No vault on this server yet. Add one with:  sudo cryptbridge vault add"
		fi
		return 0
	fi
	for id in $ids; do
		select_vault "$id"
		case "$(vault_state)" in
		running) ok "$CB_VAULT_NAME ($(service_title)): running, check code $CB_FINGERPRINT" ;;
		off) printf '  - %s (%s): switched off\n' "$CB_VAULT_NAME" "$(service_title)" ;;
		*) warn "$CB_VAULT_NAME ($(service_title)): not running. See: journalctl -u $(vault_unit)" ;;
		esac
	done
}

# vault_state — "off" (switched off), "running" (mounted) or "failed" for
# the selected vault.
vault_state() {
	if [[ "$CB_VAULT_STATE" == "off" ]]; then
		echo off
	elif [[ "$(systemctl is-active "$(vault_unit)" 2>/dev/null || true)" == "active" ]] && is_mounted "$(vault_dir)"; then
		echo running
	else
		echo failed
	fi
}

restart_services() {
	load_settings
	local units=() id
	for id in $(vault_ids); do
		select_vault "$id"
		if [[ "$CB_VAULT_STATE" == "on" ]]; then
			units+=("$(vault_unit)")
		fi
	done
	units+=(rclone-webdav.service)
	# Switched off, Caddy stays off: it would take ports 80 and 443 back.
	[[ "$CB_STREAMING" == "off" ]] || units+=(caddy.service)
	systemctl restart "${units[@]}"
	ok "Restarted"
}

# switch_streaming on|off — Caddy alone holds ports 80 and 443 for the video
# players. Switched off, the ports are free for another web server (a test
# website, say) while the vault folder keeps working. The choice is kept in
# the settings, so it lasts through a reboot, "restart" and a new setup run.
# A web server on the ports is paused for streaming and started again when
# streaming is switched off (see pause_web_server).
switch_streaming() {
	local wanted="$1"
	require_root streaming "$wanted"
	[[ -r "$SETTINGS_FILE" ]] || die "CryptBridge is not set up yet. Run:  sudo cryptbridge setup"
	load_settings
	if [[ "$wanted" == "off" ]]; then
		systemctl disable --now caddy >>"$LOG_FILE" 2>&1 || die "Could not stop Caddy. See: journalctl -u caddy"
		CB_STREAMING="off"
		save_settings
		ok "Streaming switched off: ports 80 and 443 are free, e.g. for a website."
		resume_web_servers ask
		info "The vault folder keeps working. Switch streaming on again in the menu or with:  sudo cryptbridge streaming on"
		return
	fi
	# Without the setup's Caddyfile, Caddy would serve its own start page.
	grep -qs "^$MARKER" "$CADDYFILE" || die "HTTPS has not been set up yet. Free ports 80 and 443, then run:  sudo cryptbridge setup"
	if ! offer_to_pause_web_servers; then
		# Pausing a web server is no use while another program keeps the ports.
		resume_web_servers now
		die "Streaming stays off until ports 80 and 443 are free."
	fi
	if ! systemctl enable --now caddy >>"$LOG_FILE" 2>&1; then
		give_ports_back
		die "Caddy did not start. See: journalctl -u caddy"
	fi
	CB_STREAMING="on"
	save_settings
	ok "Streaming switched on: https://$CB_ADDRESS"
}

menu() {
	local choice streaming_item next_streaming
	while true; do
		show_status
		if [[ "$CB_STREAMING" == "off" ]]; then
			streaming_item="Switch streaming on" next_streaming="on"
		else
			streaming_item="Switch streaming off (frees ports 80 and 443, e.g. for a website)" next_streaming="off"
		fi
		printf '\n'
		info "1) Streaming settings"
		info "2) $streaming_item"
		info "3) Vaults: add, switch on/off, delete"
		info "4) Restart CryptBridge"
		info "5) Recent log of the vaults"
		info "6) Run the setup again (change settings)"
		info "7) Uninstall"
		info "0) Close"
		ask choice "Choice" "0"
		case "$choice" in
		1) show_streaming_settings ;;
		# "|| true": a refusal (a port in use) is explained already and must
		# not end the menu.
		2) sudo "$SELF_INSTALLED" streaming "$next_streaming" || true ;;
		3)
			vaults_menu
			continue
			;;
		4) sudo "$SELF_INSTALLED" restart ;;
		5) journalctl -u "${VAULT_UNIT_PREFIX}*" -u rclone-webdav -n 40 --no-pager || sudo journalctl -u "${VAULT_UNIT_PREFIX}*" -u rclone-webdav -n 40 --no-pager ;;
		6) sudo "$SELF_INSTALLED" setup ;;
		7) sudo "$SELF_INSTALLED" uninstall ;;
		0 | "") return ;;
		esac
		press_enter
	done
}

# vaults_menu — the list of vaults; choosing one offers what can be done
# with it. Every action runs as a command of its own (with sudo), so a
# refusal ends that command, not the menu.
vaults_menu() {
	local choice ids i
	while true; do
		mapfile -t ids < <(vault_ids)
		printf '\n%sVaults%s (one folder each in %s)\n' "$BOLD" "$RESET" "${CB_MOUNT_DIR:-?}"
		for i in "${!ids[@]}"; do
			select_vault "${ids[i]}"
			info "$((i + 1))) $CB_VAULT_NAME ($(service_title)): $(vault_state_text)"
		done
		info "a) Add a vault (paste a transfer block from the Windows program)"
		info "0) Back"
		ask choice "Choice" "0"
		case "$choice" in
		0 | "") return ;;
		a | A)
			sudo "$SELF_INSTALLED" vault add || true
			press_enter
			;;
		*)
			if [[ "$choice" =~ ^[1-9][0-9]*$ ]] && ((choice <= ${#ids[@]})); then
				select_vault "${ids[choice - 1]}"
				vault_menu
			else
				warn "Please choose a number from the list, a or 0."
			fi
			;;
		esac
	done
}

# vault_menu — what can be done with the selected vault.
vault_menu() {
	local choice switch_item next_state
	if [[ "$CB_VAULT_STATE" == "off" ]]; then
		switch_item="Switch on" next_state="on"
	else
		switch_item="Switch off (video players and the file manager no longer show it)" next_state="off"
	fi
	printf '\n%s%s%s (%s, check code %s)\n' "$BOLD" "$CB_VAULT_NAME" "$RESET" "$(service_title)" "$CB_FINGERPRINT"
	info "1) $switch_item"
	info "2) Delete from this server (your files in the cloud stay)"
	info "0) Back"
	ask choice "Choice" "0"
	case "$choice" in
	1)
		sudo "$SELF_INSTALLED" vault "$next_state" "$CB_VAULT_NAME" || true
		press_enter
		;;
	2)
		sudo "$SELF_INSTALLED" vault delete "$CB_VAULT_NAME" || true
		press_enter
		;;
	esac
}

# vault_state_text — the state of the selected vault in words.
vault_state_text() {
	case "$(vault_state)" in
	running) echo "running" ;;
	off) echo "switched off" ;;
	*) echo "not running" ;;
	esac
}

press_enter() {
	read -r -p "    Press Enter …" _ <"$TTY"
}

uninstall() {
	require_root uninstall
	load_settings
	step "Uninstall CryptBridge"
	info "Your files in the cloud are NOT touched."
	confirm "Remove the CryptBridge services from this server?" n || exit 0
	systemctl disable --now rclone-webdav.service >>"$LOG_FILE" 2>&1 || true
	local id
	for id in $(vault_ids); do
		select_vault "$id"
		systemctl disable --now "$(vault_unit)" >>"$LOG_FILE" 2>&1 || true
		rm -f "$UNIT_DIR/$(vault_unit)"
		rmdir "$(vault_dir)" 2>/dev/null || true # empty once unmounted
	done
	# The one vault of CryptBridge 0.8, if the setup never updated it.
	systemctl disable --now rclone-mount.service >>"$LOG_FILE" 2>&1 || true
	rm -f "$OLD_MOUNT_UNIT" "$WEBDAV_UNIT" "$F2B_FILTER" "$F2B_JAIL"
	systemctl daemon-reload
	systemctl reload-or-restart fail2ban >>"$LOG_FILE" 2>&1 || true
	if [[ -f "$CADDYFILE.before-cryptbridge" ]]; then
		mv "$CADDYFILE.before-cryptbridge" "$CADDYFILE"
		systemctl reload-or-restart caddy >>"$LOG_FILE" 2>&1 || true
	elif grep -qs "^$MARKER" "$CADDYFILE"; then
		systemctl disable --now caddy >>"$LOG_FILE" 2>&1 || true
	fi
	resume_web_servers ask
	# A website on the ports needs them open for its visitors.
	local close_ports=y
	if [[ -n "$(web_port_users)" ]]; then
		info "Another web server uses ports 80 and 443 (a website, say): keep them open for it."
		close_ports=n
	fi
	if confirm "Close ports 80 and 443 in the firewall?" "$close_ports"; then
		ufw delete allow 80/tcp >>"$LOG_FILE" 2>&1 || true
		ufw delete allow 443/tcp >>"$LOG_FILE" 2>&1 || true
	fi
	if [[ -n "${CB_HOME:-}" || -n "${CB_USER:-}" ]]; then
		local home
		home="$(getent passwd "${CB_USER:-}" | cut -d: -f6 || true)"
		rm -f "$home"/*/cryptbridge-vault.desktop "$home"/*/cryptbridge.desktop
	fi
	if confirm "Delete the cloud sign-in and the vault keys on this server ($CONF_DIR)?" n; then
		rm -rf "$CONF_DIR"
	fi
	if confirm "Delete the cache ($CACHE_DIR)? Files not yet uploaded would be lost." n; then
		rm -rf "$CACHE_DIR"
	fi
	rm -f "$SELF_INSTALLED"
	ok "CryptBridge removed"
}

summary() {
	printf '\n%s%s CryptBridge is ready %s\n' "$BOLD" "$GREEN" "$RESET"
	info "Your vaults, one folder each in $CB_MOUNT_DIR (on the desktop: 'CryptBridge Vault'):"
	show_vaults
	info "The check code of each vault must be the same as in the Windows program."
	show_streaming_settings
	if ((HTTPS_POSSIBLE == 0)); then
		printf '\n'
		warn "No HTTPS yet: port 80 or 443 is in use by another program, so these settings do not work yet."
		warn "Free the ports, then run:  sudo cryptbridge setup"
	elif ((ADDRESS_IS_IP)); then
		printf '\n'
		warn "With an IP address, only players that ignore certificates connect. A name from duckdns.org fixes that:"
		warn "point it to this server, then run:  sudo cryptbridge setup"
	fi
	printf '\n'
	info "Later: run 'cryptbridge' (or the 'CryptBridge' icon) for status, vaults, restart and settings."
	info "Another vault: create its transfer block in the Windows program, then run:  sudo cryptbridge vault add"
	info "Moving to a new server: run this setup there, then point your host name to the new server."
}

# ==========================================================================
# Main
# ==========================================================================

setup() {
	require_root setup
	install -d -m 0750 "$(dirname "$LOG_FILE")"
	: >>"$LOG_FILE"
	printf '%sCryptBridge setup %s%s\n' "$BOLD" "$CRYPTBRIDGE_VERSION" "$RESET"
	info "Log file: $LOG_FILE"
	load_settings
	check_system
	check_web_ports
	choose_user
	install_packages
	install_rclone
	update_single_vault
	choose_vaults
	choose_cache
	choose_webdav_account
	choose_address
	write_units
	start_services
	setup_firewall
	check_https
	setup_fail2ban
	install_self
	setup_desktop
	save_settings
	summary
}

# default_command — what runs without a command: the installed "cryptbridge"
# opens the menu, as the texts and the desktop icon promise; the downloaded
# setup.sh sets up (also when it is run again to update a server).
default_command() {
	if [[ "$(readlink -f "$0" 2>/dev/null || true)" == "$SELF_INSTALLED" ]]; then
		echo menu
	else
		echo setup
	fi
}

main() {
	case "${1:-$(default_command)}" in
	setup) setup ;;
	menu) menu ;;
	status) show_status ;;
	streaming)
		case "${2:-}" in
		"") show_streaming_settings ;;
		on | off) switch_streaming "$2" ;;
		*) die "Unknown choice '$2'. Use: cryptbridge streaming (shows the settings), or: sudo cryptbridge streaming on|off" ;;
		esac
		;;
	infuse) show_streaming_settings ;; # the name of "streaming" up to CryptBridge 0.5
	vault)
		case "${2:-}" in
		"" | list) list_vaults ;;
		add) vault_add ;;
		on | off) switch_vault "$2" "${3:-}" ;;
		delete) delete_vault "${3:-}" ;;
		*) die "Unknown choice '$2'. Use: cryptbridge vault (lists them), or: sudo cryptbridge vault add | on NAME | off NAME | delete NAME" ;;
		esac
		;;
	restart)
		require_root restart
		restart_services
		;;
	uninstall) uninstall ;;
	version) echo "$CRYPTBRIDGE_VERSION" ;;
	*) die "Unknown command '$1'. Use: setup, menu, status, vault [add|on|off|delete], streaming [on|off], restart, uninstall, version" ;;
	esac
}

# Run only when executed, not when the tests read the functions in.
# (When bash reads the script from a pipe, BASH_SOURCE is empty: run too.)
if [[ "${BASH_SOURCE[0]:-$0}" == "$0" ]]; then
	main "$@"
fi
