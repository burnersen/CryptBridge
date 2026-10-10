// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

"use strict";

// All dynamic text is set with textContent, never innerHTML: folder and file
// names come from pCloud and must never be able to inject markup.

const $ = (id) => document.getElementById(id);
let api = null;          // window.go.main.App, set once Wails is ready
let snapshot = null;     // last state from the engine
let busy = false;        // an action is running: ignore further clicks

// ---------- helpers ----------

function show(el, visible) { el.classList.toggle("hidden", !visible); }

function formatBytes(n) {
  if (!n && n !== 0) return "–";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function errorText(err) {
  return typeof err === "string" ? err : (err && err.message) || String(err);
}

// run executes an action once at a time and shows errors in target.
async function run(target, action) {
  if (busy) return;
  busy = true;
  document.body.style.cursor = "progress";
  try {
    return await action();
  } catch (err) {
    if (target) setNotice(target, "error", errorText(err));
  } finally {
    busy = false;
    document.body.style.cursor = "";
  }
}

function setNotice(el, kind, text) {
  el.className = "notice " + kind;
  el.textContent = text;
  show(el, true);
}

// ---------- tabs ----------

function openTab(name) {
  document.querySelectorAll(".tab").forEach((t) => t.classList.toggle("active", t.dataset.tab === name));
  document.querySelectorAll(".tab-page").forEach((p) => p.classList.toggle("active", p.id === "tab-" + name));
  if (name === "settings") loadSettings();
}

document.querySelectorAll(".tab").forEach((t) => t.addEventListener("click", () => openTab(t.dataset.tab)));
document.querySelectorAll("[data-goto]").forEach((b) => b.addEventListener("click", () => openTab(b.dataset.goto)));
document.querySelectorAll("[data-url]").forEach((b) => b.addEventListener("click", () => api && api.OpenURL(b.dataset.url)));

// ---------- status ----------

const PHASES = {
  starting: ["Starting…", "warn"],
  setup: ["Setup needed", ""],
  disconnected: ["Disconnected", ""],
  connecting: ["Connecting…", "warn"],
  connected: ["Connected", "good"],
  reconnecting: ["Reconnecting…", "warn"],
  error: ["Problem", "bad"],
};

function render(s) {
  snapshot = s;
  const [label, kind] = PHASES[s.phase] || [s.phase, ""];
  const pill = $("phase-pill");
  pill.textContent = label;
  pill.className = "pill " + kind;
  $("version").textContent = "v" + s.version;
  $("about-version").textContent = s.version;

  const connected = s.phase === "connected" || s.phase === "reconnecting";
  const letter = (s.driveLetter || "Z") + ":";
  $("drive-letter").textContent = letter;
  $("drive-letter").classList.toggle("on", s.phase === "connected");
  $("drive-state").textContent = connected ? `Connected as ${letter}` : label;
  $("drive-detail").textContent = connected ? "Your vault is open in Explorer as a network drive." :
    (s.phase === "setup" ? "" : "Click Connect to open your vault.");
  $("btn-connect").textContent = connected ? "Disconnect" : "Connect";
  $("btn-connect").disabled = s.phase === "setup" || s.phase === "starting" || s.phase === "connecting";
  $("btn-open-drive").disabled = s.phase !== "connected";

  show($("status-message"), !!s.message && (s.phase === "error" || s.phase === "reconnecting"));
  $("status-message").textContent = s.message || "";
  show($("winfsp-missing"), !s.winFsp);
  show($("rclone-missing"), !s.rclone && s.phase === "error");
  show($("setup-needed"), s.phase === "setup");

  renderUploads(s.drive || {});
  $("vault-path").textContent = s.hasVault ? s.vaultPath : "–";
  $("vault-fingerprint").textContent = s.fingerprint || "–";

  if (s.usage && s.usage.total > 0) {
    const used = s.usage.used / s.usage.total * 100;
    $("usage-bar").style.width = used.toFixed(1) + "%";
    $("usage-text").textContent = `${formatBytes(s.usage.used)} used, ${formatBytes(s.usage.free)} free of ${formatBytes(s.usage.total)}`;
  }

  renderAccount(s);
}

function renderUploads(d) {
  const pending = (d.uploadsInProgress || 0) + (d.uploadsQueued || 0);
  $("uploads-summary").textContent = pending === 0 ? "Nothing to upload." :
    `${d.uploadsInProgress || 0} uploading, ${d.uploadsQueued || 0} waiting.`;
  const list = $("transfers");
  list.replaceChildren();
  for (const t of d.transfers || []) {
    const li = document.createElement("li");
    const head = document.createElement("div");
    head.className = "tname";
    const name = document.createElement("span");
    name.textContent = t.name;
    const pct = document.createElement("span");
    pct.textContent = `${t.percentage}% · ${formatBytes(t.speed)}/s`;
    head.append(name, pct);
    const bar = document.createElement("div");
    bar.className = "bar";
    const fill = document.createElement("div");
    fill.className = "bar-fill";
    fill.style.width = t.percentage + "%";
    bar.append(fill);
    li.append(head, bar);
    list.append(li);
  }
}

$("btn-connect").addEventListener("click", () => run($("status-message"), async () => {
  const connected = snapshot && (snapshot.phase === "connected" || snapshot.phase === "reconnecting");
  if (!connected) return api.Connect();
  let pending = await api.Disconnect(false);
  if (pending > 0) {
    const ok = confirm(`${pending} file(s) are still being uploaded to pCloud.\n\n` +
      "If you disconnect now, they stay on this PC and are uploaded the next time you connect.\n\nDisconnect anyway?");
    if (ok) await api.Disconnect(true);
  }
}));
$("btn-open-drive").addEventListener("click", () => run($("status-message"), () => api.OpenDrive()));
$("winfsp-download").addEventListener("click", () => api.OpenWinFspDownload());

// ---------- account ----------

function renderAccount(s) {
  const region = s.pcloudHost === "eapi.pcloud.com" ? "EU" : (s.pcloudHost ? "US" : "");
  $("account-state").textContent = s.signedIn ? `Signed in (pCloud ${region} region).` : "Not signed in.";
  $("btn-signin").textContent = s.signedIn ? "Sign in again" : "Sign in to pCloud";
  show($("btn-signout"), s.signedIn);
}

let signInLink = "";
$("btn-signin").addEventListener("click", () => run($("vault-result"), async () => {
  show($("signin-waiting"), true);
  show($("signin-link-row"), false);
  try {
    await api.SignIn();
  } finally {
    show($("signin-waiting"), false);
  }
}));
$("btn-signin-cancel").addEventListener("click", () => api.CancelSignIn());
$("signin-link").addEventListener("click", () => signInLink && api.OpenURL(signInLink));
$("btn-signout").addEventListener("click", () => run($("vault-result"), async () => {
  if (confirm("Sign out of pCloud? The vault keys are kept.")) await api.SignOut();
}));

// ---------- vault ----------

function vaultMode() {
  return document.querySelector("input[name=vault-mode]:checked").value;
}

function applyMode() {
  const isNew = vaultMode() === "new";
  document.querySelectorAll(".only-new").forEach((el) => show(el, isNew));
  document.querySelectorAll(".only-existing").forEach((el) => show(el, !isNew));
  show($("vault-result"), false);
}
document.querySelectorAll("input[name=vault-mode]").forEach((r) => r.addEventListener("change", applyMode));

document.querySelectorAll("[data-reveal]").forEach((b) => b.addEventListener("click", () => {
  const input = $(b.dataset.reveal);
  input.type = input.type === "password" ? "text" : "password";
}));
document.querySelectorAll("[data-generate]").forEach((b) => b.addEventListener("click", async () => {
  const input = $(b.dataset.generate);
  input.value = await api.NewPassword();
  input.type = "text"; // the user has to see it to write it down
}));

function vaultConfig() {
  const isNew = vaultMode() === "new";
  return {
    path: $("vault-folder").value.trim(),
    filenameEncryption: isNew ? "standard" : $("adv-encryption").value,
    directoryNameEncryption: isNew ? true : $("adv-dirnames").checked,
    filenameEncoding: isNew ? "base32" : $("adv-encoding").value,
    suffix: isNew ? ".bin" : ($("adv-suffix").value || ".bin"),
    password: $("vault-password").value,
    password2: $("vault-salt").value,
  };
}

const VERDICTS = {
  empty: ["info", "The folder is empty (or does not exist yet): a new vault can be created here."],
  match: ["good", "The keys open this vault."],
  mismatch: ["error", "These keys do NOT open the vault in this folder. Check password, salt and the advanced settings."],
  unverifiable: ["warn", "This vault does not encrypt file names, so the keys cannot be checked."],
};

function showCheck(check) {
  const [kind, text] = VERDICTS[check.verification.verdict] || ["info", check.verification.verdict];
  const code = check.fingerprint ? `  Check code: ${check.fingerprint}` : "";
  setNotice($("vault-result"), kind, text + code);
}

$("btn-check").addEventListener("click", () => run($("vault-result"), async () => {
  setNotice($("vault-result"), "info", "Checking…");
  showCheck(await api.CheckVault(vaultConfig()));
}));

$("btn-save-vault").addEventListener("click", () => run($("vault-result"), async () => {
  const create = vaultMode() === "new";
  if (create && !confirm("Have you written down the password and the salt?\n\nWithout them the vault can never be opened again.")) return;
  setNotice($("vault-result"), "info", create ? "Creating the vault…" : "Checking and opening the vault…");
  let check;
  try {
    check = await api.UseVault(vaultConfig(), create, false);
  } catch (err) {
    const text = errorText(err);
    if (!text.includes("cannot be checked") ||
        !confirm(text + "\n\nUse it anyway? Make sure the keys are right — wrong keys would split the vault.")) throw err;
    check = await api.UseVault(vaultConfig(), create, true);
  }
  showCheck(check);
  $("vault-result").textContent += "  — Saved. You can connect the drive on the Status tab.";
}));

$("btn-find").addEventListener("click", () => run($("vault-result"), async () => {
  setNotice($("vault-result"), "info", "Searching pCloud for vaults… (only names are read, nothing is downloaded)");
  const candidates = await api.FindVaults();
  const list = $("candidates");
  list.replaceChildren();
  for (const c of candidates || []) {
    const li = document.createElement("li");
    li.textContent = `${c.path}  (${c.entries} entries${c.confirmed ? ", confirmed rclone vault" : ""})`;
    li.addEventListener("click", () => { $("vault-folder").value = c.path; show(list, false); });
    list.append(li);
  }
  show(list, (candidates || []).length > 0);
  setNotice($("vault-result"), "info", (candidates || []).length === 0 ?
    "No vault found in your pCloud." : "Click a folder to use it, then enter password and salt.");
}));

// ---------- rclone download ----------

$("btn-download-rclone").addEventListener("click", () => run($("status-message"), async () => {
  show($("download-bar"), true);
  $("download-fill").style.width = "0%";
  try {
    await api.DownloadRclone();
  } finally {
    show($("download-bar"), false);
  }
}));

// ---------- transfer to the Linux server ----------

$("btn-transfer").addEventListener("click", () => run($("vault-result"), async () => {
  const t = await api.CreateTransfer();
  $("transfer-code").textContent = t.code;
  $("transfer-block").value = t.block;
  $("transfer-result").textContent = "";
  show($("transfer-area"), true);
}));
$("btn-copy-block").addEventListener("click", async () => {
  await api.CopyText($("transfer-block").value);
  $("transfer-result").textContent = "Copied. Paste it into the setup on the server.";
});
$("btn-save-block").addEventListener("click", () => run(null, async () => {
  const path = await api.SaveTransfer($("transfer-block").value);
  if (path) $("transfer-result").textContent = "Saved: " + path;
}));

// ---------- emergency sheet ----------

// rcloneCommands: how to open the vault with plain rclone. "--obscure" is
// essential: without it rclone stores a password that merely LOOKS obscured
// (any 32 letters and digits do) unchanged, and the vault does not open
// (measured with rclone 1.75.1).
function rcloneCommands(d) {
  const lines = [
    `rclone config create cbpcloud pcloud hostname=${d.pcloudHost}`,
    "    (a browser opens: sign in to pCloud)",
    `rclone config create cbvault crypt remote="cbpcloud:${d.vaultPath}" ` +
      `filename_encryption=${d.filenameEncryption} directory_name_encryption=${d.directoryNameEncryption} ` +
      `filename_encoding=${d.filenameEncoding} suffix=${d.suffix} password="${d.password}"` +
      (d.salt ? ` password2="${d.salt}"` : "") + " --obscure",
    "    (keep --obscure: without it rclone may store the password wrongly)",
    "rclone ls cbvault:",
  ];
  return lines.join("\n");
}

$("btn-sheet").addEventListener("click", () => run($("vault-result"), async () => {
  const d = await api.EmergencySheet();
  $("s-created").textContent = d.createdAt;
  $("s-region").textContent = `${d.region} (${d.pcloudHost})`;
  $("s-path").textContent = d.vaultPath;
  $("s-password").textContent = d.password;
  $("s-salt").textContent = d.salt || "(none)";
  $("s-encryption").textContent = d.filenameEncryption;
  $("s-dirnames").textContent = d.directoryNameEncryption ? "yes" : "no";
  $("s-encoding").textContent = d.filenameEncoding;
  $("s-suffix").textContent = d.suffix;
  $("s-fingerprint").textContent = d.fingerprint || "–";
  $("s-rclone").textContent = rcloneCommands(d);
  show($("sheet"), true);
}));

function closeSheet() {
  // Do not leave the keys in the page after the sheet is closed.
  for (const id of ["s-password", "s-salt", "s-rclone"]) $(id).textContent = "";
  show($("sheet"), false);
}
$("btn-sheet-print").addEventListener("click", () => window.print());
$("btn-sheet-close").addEventListener("click", closeSheet);

// ---------- settings ----------

async function loadSettings() {
  if (!api || !snapshot) return;
  const letters = await api.FreeLetters();
  const select = $("opt-letter");
  select.replaceChildren();
  for (const l of letters.sort()) {
    const o = document.createElement("option");
    o.value = l;
    o.textContent = l + ":";
    select.append(o);
  }
  select.value = snapshot.driveLetter;
  $("opt-cache-dir").value = snapshot.cacheDir;
  $("opt-cache-max").value = snapshot.cacheMaxSize;
  $("opt-cache-free").value = snapshot.cacheMinFreeSpace;
  $("opt-autoconnect").checked = snapshot.autoConnect;
  $("opt-autoreconnect").checked = snapshot.autoReconnect;
  $("opt-autostart").checked = await api.AutostartEnabled();
  $("settings-result").textContent = "";
}

$("btn-browse-cache").addEventListener("click", async () => {
  const folder = await api.ChooseFolder($("opt-cache-dir").value);
  if (folder) $("opt-cache-dir").value = folder;
});

$("btn-save-settings").addEventListener("click", () => run(null, async () => {
  const result = $("settings-result");
  try {
    await api.UpdateOptions({
      letter: $("opt-letter").value,
      cacheDir: $("opt-cache-dir").value.trim(),
      cacheMaxSize: $("opt-cache-max").value.trim(),
      cacheMinFreeSpace: $("opt-cache-free").value.trim(),
      autoConnect: $("opt-autoconnect").checked,
      autoReconnect: $("opt-autoreconnect").checked,
    });
    await api.SetAutostart($("opt-autostart").checked);
    result.textContent = "Saved.";
  } catch (err) {
    result.textContent = errorText(err);
  }
}));

// ---------- start ----------

async function init() {
  // Wails injects its bridge shortly after the page loads.
  for (let i = 0; i < 100 && !(window.go && window.go.main && window.runtime); i++) {
    await new Promise((r) => setTimeout(r, 50));
  }
  api = window.go.main.App;
  window.runtime.EventsOn("snapshot", render);
  window.runtime.EventsOn("signin-link", (link) => { signInLink = link; show($("signin-link-row"), true); });
  window.runtime.EventsOn("download-progress", (pct) => { $("download-fill").style.width = pct + "%"; });
  applyMode();
  $("vault-folder").value = "CryptBridge";
  const s = await api.GetSnapshot();
  render(s);
  if (s.phase === "setup") openTab("vault");
}

init();
