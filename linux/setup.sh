#!/usr/bin/env bash
# CryptBridge (https://github.com/burnersen/CryptBridge)
# Copyright (C) 2026 burnersen
# SPDX-License-Identifier: GPL-3.0-only
#
# CryptBridge Linux setup.
#
# Turns a fresh Ubuntu/Debian server into a streaming server for an
# rclone-crypt encrypted vault in pCloud, Google Drive, OneDrive or Dropbox:
#
#   cloud <-> rclone mount (rclone-mount.service) -> ~/CryptBridge (read/write in the desktop)
#                 \-> rclone serve webdav (rclone-webdav.service, read-only) <- Caddy (HTTPS) <- video player
#
# Run it again at any time to repair or change the setup. After the first run
# it is also installed as the command "cryptbridge" (status, restart, ...).
#
#   sudo bash setup.sh            set up or change CryptBridge
#   cryptbridge                   menu: status, streaming settings, restart, ...
#   sudo cryptbridge uninstall    remove CryptBridge (keeps your files in the cloud)

set -Eeuo pipefail
# Tool output is parsed below; it must not depend on the system language.
export LC_ALL=C

readonly CRYPTBRIDGE_VERSION="0.8.1"
readonly MIN_RCLONE="1.75.0"

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
readonly RCLONE_CONF="$RCLONE_DIR/rclone.conf"
readonly OLD_RCLONE_CONF="$CONF_DIR/rclone.conf" # up to CryptBridge 0.7
readonly HTPASSWD="$CONF_DIR/htpasswd"
readonly SETTINGS_FILE="$CONF_DIR/settings"
readonly CACHE_DIR="$ROOT/var/cache/cryptbridge"
readonly MOUNT_UNIT="$ROOT/etc/systemd/system/rclone-mount.service"
readonly WEBDAV_UNIT="$ROOT/etc/systemd/system/rclone-webdav.service"
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
readonly -A SERVICE_TITLES=([pcloud]="pCloud" [drive]="Google Drive" [onedrive]="OneDrive" [dropbox]="Dropbox")
readonly WEBDAV_ADDR="127.0.0.1:8080"
readonly CHECK_FILE=".cryptbridge-vault"
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
	{
		echo "$MARKER — settings of the last setup run (no secrets here)"
		local name
		for name in CB_USER CB_MOUNT_DIR CB_VAULT_PATH CB_ADDRESS CB_WEBDAV_USER CB_CACHE_MAX CB_CACHE_MIN_FREE CB_FINGERPRINT; do
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
		id "$CB_USER" >/dev/null 2>&1 && break
		warn "User '$CB_USER' does not exist."
	done
	CB_GROUP="$(id -gn "$CB_USER")"
	CB_HOME="$(getent passwd "$CB_USER" | cut -d: -f6)"
	CB_MOUNT_DIR="${CB_MOUNT_DIR:-$CB_HOME/CryptBridge}"
	ok "Vault folder for $CB_USER: $CB_MOUNT_DIR"
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

# version_ge A B — true if version A >= B (numbers compared, not text):
# "B then A" is already in version order exactly when A >= B.
version_ge() {
	printf '%s\n%s\n' "$2" "$1" | sort -V -C
}

rclone_version() {
	local out
	out="$("$1" version 2>/dev/null)" || return 1
	[[ "$out" =~ rclone\ v([0-9]+\.[0-9]+\.[0-9]+) ]] || return 1
	printf '%s' "${BASH_REMATCH[1]}"
}

install_rclone() {
	step "rclone"
	local current=""
	if [[ -x "$RCLONE_BIN" ]]; then
		current="$(rclone_version "$RCLONE_BIN" || true)"
	fi
	if [[ -n "$current" ]] && version_ge "$current" "$MIN_RCLONE"; then
		ok "rclone $current is installed"
		return
	fi

	local latest tmp zip sums expected actual
	latest="$(curl -fsSL https://downloads.rclone.org/version.txt)" || die "Cannot reach downloads.rclone.org."
	latest="${latest#rclone v}"
	[[ "$latest" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "Unexpected answer from downloads.rclone.org: $latest"
	info "Downloading rclone $latest …"
	tmp="$(mktemp -d)"
	zip="rclone-v$latest-linux-$ARCH.zip"
	curl -fsSL "https://downloads.rclone.org/v$latest/$zip" -o "$tmp/$zip" || die "Download of rclone failed."
	sums="$(curl -fsSL "https://downloads.rclone.org/v$latest/SHA256SUMS")" || die "Download of the rclone checksums failed."
	expected="$(awk -v f="$zip" '$2 == f {print $1}' <<<"$sums")"
	actual="$(sha256sum "$tmp/$zip" | awk '{print $1}')"
	[[ -n "$expected" && "$expected" == "$actual" ]] || die "Checksum of the rclone download does not match — not installed."
	unzip -q -o "$tmp/$zip" -d "$tmp"
	install -m 0755 "$tmp/rclone-v$latest-linux-$ARCH/rclone" "$RCLONE_BIN"
	rm -rf "$tmp"
	ok "rclone $latest installed (checksum verified)"
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

# Values filled by one of the ways in get_keys. CLOUD_SETTINGS are rclone's
# settings of the cloud account, one "key = value" per line, without the type
# (that is CB_SERVICE).
CB_SERVICE=""
CLOUD_SETTINGS=""
PCLOUD_TOKEN=""
CB_VAULT_PATH="${CB_VAULT_PATH:-}"
VAULT_ENCRYPTION="standard"
VAULT_DIRNAMES="true"
VAULT_ENCODING="base32"
VAULT_SUFFIX=".bin"
VAULT_PASSWORD=""
VAULT_SALT=""
EXPECTED_FINGERPRINT=""

# A valid cloud sign-in in a transfer block: a service name, and settings
# that can only become "key = value" lines of the rclone configuration —
# a line break in a value could add settings or remotes of its own.
# shellcheck disable=SC2016 # jq's code, not the shell's
readonly VALID_CLOUD_SIGN_IN='
	(.service | type == "string" and test("^[a-z0-9]+$"))
	and (.settings | type == "object")
	and (.settings.token | type == "string" and length > 0)
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

get_keys() {
	step "Cloud sign-in and vault keys"
	copy_old_rclone_conf
	if [[ -s "$RCLONE_CONF" ]] && confirm "Keep the cloud sign-in and vault keys from the last setup?" y; then
		KEEP_KEYS=1
		ok "Keeping the existing keys"
		return
	fi
	KEEP_KEYS=0
	info "How do you want to bring the keys to this server?"
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
	info "(Google Drive, OneDrive and Dropbox: please use the transfer block.)"
	ask_secret PCLOUD_TOKEN "pCloud token"
	[[ "$PCLOUD_TOKEN" == '{"access_token"'* ]] || die "That does not look like an rclone pCloud token."
	CB_SERVICE="pcloud"
	CLOUD_SETTINGS="" # configure_remotes finds the pCloud server
	ask CB_VAULT_PATH "Folder of the vault in pCloud" "${CB_VAULT_PATH:-CryptBridge}"
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
# folder under the new name. The old file stays until the services use the
# new one (remove_old_rclone_conf): a setup ended before that leaves them
# working as they were.
copy_old_rclone_conf() {
	[[ -f "$OLD_RCLONE_CONF" ]] || return 0
	prepare_rclone_dir
	[[ -s "$RCLONE_CONF" ]] && return 0
	sed -e "s/^\[$OLD_CLOUD_REMOTE\]\$/[$CLOUD_REMOTE]/" \
		-e "s/^remote = $OLD_CLOUD_REMOTE:/remote = $CLOUD_REMOTE:/" "$OLD_RCLONE_CONF" | write_private "$RCLONE_CONF"
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
	if [[ "$KEEP_KEYS" == 1 ]]; then
		read_kept_keys
		check_cloud_access
		return
	fi
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

# check_cloud_access — the cloud must accept the sign-in. The parts of
# rclone's messages are the ones the Windows program measured (see
# windows/internal/cloud/problem.go and pcloud.go); a missing network comes
# first, as a token renewal can fail for want of it too.
check_cloud_access() {
	local out title free
	title="$(service_title)"
	if ! out="$(rc about "$CLOUD_REMOTE:" 2>&1)"; then
		case "$out" in
		*"dial tcp"* | *"i/o timeout"* | *"TLS handshake timeout"*)
			die "$title cannot be reached. Please check the internet connection of this server: $out" ;;
		*"(2094)"* | *"(2095)"* | *invalid_grant* | *"Error 401"* | *InvalidAuthenticationToken* | *invalid_access_token* | *expired_access_token*)
			die "$title no longer accepts this sign-in. Sign in again (in the Windows program) and bring the keys again." ;;
		*invalid_client* | *unauthorized_client*)
			die "$title does not accept your own app. Check its client ID and secret in the Windows program and bring the keys again." ;;
		*) die "$title cannot be reached: $out" ;;
		esac
	fi
	free="$(awk '/^Free:/{print $2 " " $3}' <<<"$out")"
	ok "$title reached${free:+: free $free}"
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
# Cache, WebDAV account, address
# ==========================================================================

choose_cache() {
	step "Cache on this server"
	install -d -m 0750 -o "$CB_USER" -g "$CB_GROUP" "$CACHE_DIR"
	local free_gb default_max
	free_gb="$(df -BG --output=avail "$CACHE_DIR" | tail -n1 | tr -dc '0-9')"
	default_max=$((free_gb / 2))
	((default_max > DEFAULT_CACHE_MAX_GB)) && default_max=$DEFAULT_CACHE_MAX_GB
	((default_max < 2)) && die "Only ${free_gb} GB free on the disk — not enough for the cache."
	info "Free disk space: ${free_gb} GB. Films are cached while they play; the cache is cleaned automatically."
	info "Files you copy into the vault folder are kept here until they are uploaded."
	ask CB_CACHE_MAX "Cache size (soft limit)" "${CB_CACHE_MAX:-${default_max}G}"
	ask CB_CACHE_MIN_FREE "Always keep this much disk space free" "${CB_CACHE_MIN_FREE:-5G}"
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

choose_address() {
	step "Address of this server"
	info "Video players reach this server over HTTPS. A free certificate is fetched automatically;"
	info "that needs a name that points to this server: your own domain, or a free name"
	info "from duckdns.org (e.g. myvault.duckdns.org — set its IP to this server's IP)."
	info "Host names of your provider (…supersrv.de, …contaboserver.net) often do NOT work:"
	info "all customers share one Let's Encrypt limit of 50 certificates per week."
	ask CB_ADDRESS "Domain or host name of this server" "${CB_ADDRESS:-}"
	CB_ADDRESS="${CB_ADDRESS,,}"
	[[ "$CB_ADDRESS" =~ ^[a-z0-9.-]+$ && "$CB_ADDRESS" == *.* ]] || die "Please enter a host name such as myvault.duckdns.org."
	local resolved mine
	# "|| true": an unanswered query is handled below, not by set -e.
	resolved="$(public_dns_ipv4 "$CB_ADDRESS" || true)"
	mine="$(public_ipv4 || true)"
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

write_units() {
	step "Services"
	# On a repeated setup the vault is still mounted, and root may not even
	# look into the user's FUSE mount: only create the folder when it is free.
	if ! is_mounted; then
		install -d -m 0755 -o "$CB_USER" -g "$CB_GROUP" "$CB_MOUNT_DIR"
	fi

	cat >"$MOUNT_UNIT" <<EOF
$MARKER
[Unit]
Description=CryptBridge: encrypted vault in $(service_title) at $CB_MOUNT_DIR
Documentation=https://github.com/burnersen/CryptBridge
Wants=network-online.target
After=network-online.target
# WebDAV stops together with the mount (BindsTo there). Upholds brings it back
# once the mount runs again — without it, WebDAV stayed off after rclone had
# crashed and was restarted (measured).
Upholds=rclone-webdav.service
StartLimitIntervalSec=0

[Service]
Type=notify
User=$CB_USER
Group=$CB_GROUP
# A crash leaves a dead mount behind ("Transport endpoint is not connected"); clear it first.
ExecStartPre=/bin/sh -c 'if findmnt -rn --mountpoint "$CB_MOUNT_DIR" >/dev/null; then /usr/bin/fusermount3 -uz "$CB_MOUNT_DIR"; fi'
# The check file stays in the vault for the key check, but neither the folder
# nor WebDAV (which serves the folder) shows it: people took it for clutter.
ExecStart=$RCLONE_BIN mount $VAULT_REMOTE: $CB_MOUNT_DIR \\
  --exclude /$CHECK_FILE \\
  --config $RCLONE_CONF \\
  --cache-dir $CACHE_DIR \\
  --vfs-cache-mode $CACHE_MODE \\
  --vfs-cache-max-size $CB_CACHE_MAX \\
  --vfs-cache-min-free-space $CB_CACHE_MIN_FREE \\
  --buffer-size $BUFFER_SIZE \\
  --vfs-read-chunk-size $CHUNK_SIZE \\
  --vfs-read-chunk-size-limit $CHUNK_SIZE_LIMIT \\
  --vfs-read-ahead $READ_AHEAD \\
  --dir-cache-time $DIR_CACHE_TIME \\
  --poll-interval $POLL_INTERVAL \\
  --umask 022 \\
  --log-level INFO
ExecStop=/usr/bin/fusermount3 -uz $CB_MOUNT_DIR
Restart=on-failure
RestartSec=10
TimeoutStopSec=60
# No PrivateTmp/ProtectSystem/ProtectHome here: they give the service its own
# mount namespace, and the vault would be invisible in the desktop.
# No NoNewPrivileges either: it would block the setuid helper fusermount3.

[Install]
WantedBy=multi-user.target
EOF

	cat >"$WEBDAV_UNIT" <<EOF
$MARKER
[Unit]
Description=CryptBridge: WebDAV for video players (read-only)
Documentation=https://github.com/burnersen/CryptBridge
# Never serve the empty mount point: stop together with the mount.
BindsTo=rclone-mount.service
After=rclone-mount.service
StartLimitIntervalSec=0

[Service]
Type=simple
User=$CB_USER
Group=$CB_GROUP
ExecStartPre=/usr/bin/timeout 30 /bin/sh -c 'until findmnt -rn --mountpoint "$CB_MOUNT_DIR" >/dev/null; do sleep 1; done'
# Serves the mounted folder: one cache, and new files are visible at once.
# The mount below already caches, so no second cache here.
ExecStart=$RCLONE_BIN serve webdav $CB_MOUNT_DIR \\
  --config $RCLONE_CONF \\
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
	chmod 0644 "$MOUNT_UNIT" "$WEBDAV_UNIT"
	systemctl daemon-reload
	ok "Services written: rclone-mount.service, rclone-webdav.service"
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
	systemctl enable rclone-mount.service rclone-webdav.service >>"$LOG_FILE" 2>&1
	systemctl restart rclone-mount.service >>"$LOG_FILE" 2>&1 || die "The vault could not be mounted. See: journalctl -u rclone-mount"
	wait_for "vault mounted at $CB_MOUNT_DIR" 60 is_mounted
	systemctl restart rclone-webdav.service >>"$LOG_FILE" 2>&1 || die "WebDAV did not start. See: journalctl -u rclone-webdav"
	wait_for "WebDAV answering" 30 webdav_answers
	write_caddyfile
	systemctl enable caddy >>"$LOG_FILE" 2>&1
	systemctl reload-or-restart caddy >>"$LOG_FILE" 2>&1 || die "Caddy did not start. See: journalctl -u caddy"
	ok "Caddy configured for https://$CB_ADDRESS"
}

# is_mounted asks the kernel's mount table instead of looking into the
# folder: a FUSE mount of the desktop user refuses everybody else — even
# root — so "mountpoint" run as root would wrongly say "not mounted".
is_mounted() {
	findmnt -rn --mountpoint "$CB_MOUNT_DIR" >/dev/null 2>&1
}

webdav_answers() {
	local code
	code="$(curl -s -o /dev/null -w '%{http_code}' "http://$WEBDAV_ADDR/")" || return 1
	[[ "$code" == "401" ]] # answering, and refusing without password
}

# wait_for "what" SECONDS command... — shows progress instead of hanging silently
wait_for() {
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
	die "Timed out waiting for: $what"
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
	step "Checking HTTPS (the certificate can take a minute)"
	local i code=""
	printf '    '
	for ((i = 0; i < 90; i++)); do
		code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "https://$CB_ADDRESS/" || true)"
		[[ "$code" == "401" ]] && break
		printf '.'
		sleep 2
	done
	printf '\n'
	if [[ "$code" != "401" ]]; then
		warn "https://$CB_ADDRESS does not answer yet (last answer: ${code:-none})."
		explain_certificate_problem
		return
	fi
	ok "https://$CB_ADDRESS answers and asks for the password"
	if [[ -n "${WEBDAV_PASSWORD_FOR_TEST:-}" ]]; then
		code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 -u "$CB_WEBDAV_USER:$WEBDAV_PASSWORD_FOR_TEST" -X PROPFIND -H 'Depth: 1' "https://$CB_ADDRESS/")"
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
		"Your encrypted vault in $(service_title)" "xdg-open $CB_MOUNT_DIR" "drive-harddisk-encrypted" false
	write_launcher "$desktop/cryptbridge.desktop" "CryptBridge" \
		"Status, streaming settings and restart of CryptBridge" "$SELF_INSTALLED menu" "security-high" true

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
	printf '    Path:      /\n'
	printf '    User:      %s\n' "${CB_WEBDAV_USER:-?}"
	printf '    Password:  the streaming password you chose during setup\n'
	printf '    Infuse (Apple TV / iPhone): Settings -> Shares -> Add share -> WebDAV.\n'
	printf '    Tip for Infuse: Settings -> Playback/Streaming -> Buffer: "Generous".\n'
}

show_status() {
	load_settings
	local unit state
	printf '\n%sCryptBridge %s%s\n' "$BOLD" "$CRYPTBRIDGE_VERSION" "$RESET"
	for unit in rclone-mount rclone-webdav caddy fail2ban; do
		state="$(systemctl is-active "$unit" 2>/dev/null || true)"
		if [[ "$state" == "active" ]]; then ok "$unit: running"; else warn "$unit: $state"; fi
	done
	if [[ -n "${CB_MOUNT_DIR:-}" ]] && is_mounted; then
		ok "Vault folder: ${CB_MOUNT_DIR}"
	else
		warn "Vault folder ${CB_MOUNT_DIR:-?} is not mounted"
	fi
	info "Check code: ${CB_FINGERPRINT:-?}"
}

restart_services() {
	systemctl restart rclone-mount.service rclone-webdav.service caddy.service
	ok "Restarted"
}

menu() {
	local choice
	while true; do
		show_status
		printf '\n'
		info "1) Streaming settings"
		info "2) Restart CryptBridge"
		info "3) Recent log of the vault"
		info "4) Run the setup again (change keys or settings)"
		info "5) Uninstall"
		info "0) Close"
		ask choice "Choice" "0"
		case "$choice" in
		1) show_streaming_settings ;;
		2) sudo "$SELF_INSTALLED" restart ;;
		3) journalctl -u rclone-mount -u rclone-webdav -n 40 --no-pager || sudo journalctl -u rclone-mount -u rclone-webdav -n 40 --no-pager ;;
		4) sudo "$SELF_INSTALLED" setup ;;
		5) sudo "$SELF_INSTALLED" uninstall ;;
		0 | "") return ;;
		esac
		read -r -p "    Press Enter …" _ <"$TTY"
	done
}

uninstall() {
	require_root uninstall
	load_settings
	step "Uninstall CryptBridge"
	info "Your files in the cloud are NOT touched."
	confirm "Remove the CryptBridge services from this server?" n || exit 0
	systemctl disable --now rclone-webdav.service rclone-mount.service >>"$LOG_FILE" 2>&1 || true
	rm -f "$MOUNT_UNIT" "$WEBDAV_UNIT" "$F2B_FILTER" "$F2B_JAIL"
	systemctl daemon-reload
	systemctl reload-or-restart fail2ban >>"$LOG_FILE" 2>&1 || true
	if [[ -f "$CADDYFILE.before-cryptbridge" ]]; then
		mv "$CADDYFILE.before-cryptbridge" "$CADDYFILE"
		systemctl reload-or-restart caddy >>"$LOG_FILE" 2>&1 || true
	elif grep -qs "^$MARKER" "$CADDYFILE"; then
		systemctl disable --now caddy >>"$LOG_FILE" 2>&1 || true
	fi
	if confirm "Close ports 80 and 443 in the firewall?" y; then
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
	info "Vault folder on this server: $CB_MOUNT_DIR (also on the desktop: 'CryptBridge Vault')"
	info "Check code: $CB_FINGERPRINT — must be the same in the Windows program."
	show_streaming_settings
	printf '\n'
	info "Later: run 'cryptbridge' (or the 'CryptBridge' icon) for status, restart and settings."
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
	choose_user
	install_packages
	install_rclone
	get_keys
	configure_remotes
	verify_keys
	compute_fingerprint
	choose_cache
	choose_webdav_account
	choose_address
	write_units
	remove_old_rclone_conf # the services now use the new one
	start_services
	setup_firewall
	check_https
	setup_fail2ban
	install_self
	setup_desktop
	save_settings
	summary
}

main() {
	case "${1:-setup}" in
	setup) setup ;;
	menu) menu ;;
	status) show_status ;;
	streaming | infuse) show_streaming_settings ;; # "infuse": its name up to CryptBridge 0.5
	restart)
		require_root restart
		restart_services
		;;
	uninstall) uninstall ;;
	version) echo "$CRYPTBRIDGE_VERSION" ;;
	*) die "Unknown command '$1'. Use: setup, menu, status, streaming, restart, uninstall, version" ;;
	esac
}

# Run only when executed, not when the tests read the functions in.
# (When bash reads the script from a pipe, BASH_SOURCE is empty: run too.)
if [[ "${BASH_SOURCE[0]:-$0}" == "$0" ]]; then
	main "$@"
fi
