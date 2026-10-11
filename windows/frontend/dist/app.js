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
  locked: ["Locked", ""],
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
  show($("signin-problem"), !!s.signInProblem);
  $("signin-problem-text").textContent = s.signInProblem || "";
  show($("signin-problem-tip"), !s.ownPCloudApp);
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
  renderApp(s);
  renderLock(s);
}

function isConnected() {
  return !!snapshot && (snapshot.phase === "connected" || snapshot.phase === "reconnecting");
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

// disconnect removes the drive, asking first while uploads are running.
// It returns false if the user chose to keep the drive.
async function disconnect() {
  const pending = await api.Disconnect(false);
  if (pending === 0) return true;
  const ok = confirm(`${pending} file(s) are still being uploaded to pCloud.\n\n` +
    "If you disconnect now, they stay on this PC and are uploaded the next time you connect.\n\nDisconnect anyway?");
  if (ok) await api.Disconnect(true);
  return ok;
}

$("btn-connect").addEventListener("click", () => run($("status-message"), async () => {
  if (!isConnected()) return api.Connect();
  await disconnect();
}));
$("btn-open-drive").addEventListener("click", () => run($("status-message"), () => api.OpenDrive()));
$("winfsp-download").addEventListener("click", () => api.OpenWinFspDownload());

// ---------- account ----------

function renderAccount(s) {
  const region = s.pcloudHost === "eapi.pcloud.com" ? "EU" : (s.pcloudHost ? "US" : "");
  const via = s.signInAppId ? "your own pCloud app" : "rclone's shared access";
  $("account-state").textContent = s.signedIn ? `Signed in (pCloud ${region} region) through ${via}.` : "Not signed in.";
  $("btn-signin").textContent = s.signedIn ? "Sign in again" : "Sign in to pCloud";
  show($("btn-signout"), s.signedIn);
  show($("signin-hint-shared"), !s.ownPCloudApp);
  show($("signin-hint-own"), s.ownPCloudApp);
}

// signIn runs the pCloud sign-in in the browser. A mounted drive keeps
// using the old sign-in, so a connected drive is disconnected first and
// connected again afterwards — also when the sign-in fails or is
// cancelled, so that nothing is left worse than before.
async function signIn() {
  openTab("vault");
  const wasConnected = isConnected();
  if (wasConnected && !(await disconnect())) return;

  let failure = null;
  show($("signin-waiting"), true);
  show($("signin-link-row"), false);
  try {
    await api.SignIn();
  } catch (err) {
    failure = err;
  } finally {
    show($("signin-waiting"), false);
  }
  if (wasConnected) {
    try {
      await api.Connect();
    } catch (err) {
      failure = failure || err;
    }
  }
  if (failure) throw failure;
  setNotice($("vault-result"), "good", wasConnected ? "Signed in to pCloud. The drive is connected again." : "Signed in to pCloud.");
}

let signInLink = "";
$("btn-signin").addEventListener("click", () => run($("vault-result"), signIn));
$("btn-resignin").addEventListener("click", () => run($("vault-result"), signIn));
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
  const password = await askAppPassword("The transfer block hands your keys on. Please confirm with your app password.");
  if (password === null) return;
  const t = await api.CreateTransfer(password);
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
  const password = await askAppPassword("The emergency sheet shows your vault keys. Please confirm with your app password.");
  if (password === null) return;
  const d = await api.EmergencySheet(password);
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
  loadAppFields();
  show($("lock-result"), false);
}

$("btn-browse-cache").addEventListener("click", async () => {
  const folder = await api.ChooseFolder($("opt-cache-dir").value);
  if (folder) $("opt-cache-dir").value = folder;
});

// ---------- own pCloud app ----------

function renderApp(s) {
  let state = "rclone's shared access (no own app saved).";
  if (s.ownPCloudApp && s.signedIn && s.signInAppId === s.pcloudClientId) {
    state = `Your own pCloud app (client ID ${s.pcloudClientId}).`;
  } else if (s.ownPCloudApp && s.signedIn) {
    state = `Your own app (client ID ${s.pcloudClientId}) is saved, but the current sign-in was made without it. ` +
      "Sign in again to use it.";
  } else if (s.ownPCloudApp) {
    state = `Your own app (client ID ${s.pcloudClientId}) is saved and is used when you sign in.`;
  }
  $("app-state").textContent = state;
  show($("btn-remove-app"), s.ownPCloudApp);
}

function loadAppFields() {
  $("app-client-id").value = snapshot.pcloudClientId || "";
  $("app-client-secret").value = "";
  $("app-client-secret").type = "password";
  $("app-client-secret").placeholder = snapshot.ownPCloudApp ? "saved — enter it again only to change it" : "";
  show($("app-result"), false);
}

$("btn-save-app").addEventListener("click", () => run($("app-result"), async () => {
  await api.SetPCloudApp($("app-client-id").value, $("app-client-secret").value);
  render(await api.GetSnapshot()); // the snapshot event may arrive only after this
  loadAppFields();
  if (!snapshot.signedIn) {
    setNotice($("app-result"), "good", "Saved. Your app is used when you sign in on the Vault tab.");
    return;
  }
  if (confirm("Your pCloud app is saved.\n\nSign in again now, so that CryptBridge uses it? " +
      "If the drive is connected, it is disconnected for the sign-in and connected again afterwards.")) {
    await signIn();
    return;
  }
  setNotice($("app-result"), "good", "Saved. CryptBridge uses your app from the next sign-in (Vault tab → Sign in again).");
}));

$("btn-remove-app").addEventListener("click", () => run($("app-result"), async () => {
  if (!confirm("Use rclone's shared access again instead of your own pCloud app?\n\n" +
      "Your current sign-in keeps working; the shared access is used from the next sign-in.")) return;
  await api.SetPCloudApp("", "");
  render(await api.GetSnapshot());
  loadAppFields();
  setNotice($("app-result"), "info", "Removed. The next sign-in uses rclone's shared access.");
}));

// The page is not selectable, so texts to paste into pCloud's form have a
// Copy button. Line breaks of the HTML source are not part of the text.
document.querySelectorAll("[data-copy]").forEach((b) => b.addEventListener("click", async () => {
  await api.CopyText($(b.dataset.copy).textContent.replace(/\s+/g, " ").trim());
  const label = b.dataset.label || (b.dataset.label = b.textContent);
  b.textContent = "Copied";
  setTimeout(() => { b.textContent = label; }, 1500);
}));

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

// ---------- app lock ----------

let wasLocked = false;

function renderLock(s) {
  show($("lock-screen"), s.locked);
  if (s.locked && !wasLocked) {
    wipePage();
    $("unlock-password").focus();
  }
  wasLocked = s.locked;

  const letter = (s.driveLetter || "Z") + ":";
  $("lock-hint").textContent = s.unlockConnects ?
    `Enter your app password to open the vault. The drive ${letter} connects afterwards.` :
    "Enter your app password to open the vault.";
  show($("lock-status"), s.locked && !!s.message);
  $("lock-status").textContent = s.locked ? s.message || "" : "";

  show($("lock-offer"), !s.appLock && !s.lockOffered && s.hasVault && !s.locked);
  $("lock-state").textContent = s.appLock ?
    "On — CryptBridge locks itself with Windows and asks for the app password after starting." : "Off";
  show($("lock-off"), !s.appLock);
  show($("lock-on"), s.appLock);
}

// wipePage removes everything from the page that could show keys: the lock
// screen covers the window, but the page itself must not keep them either.
let cancelPasswordDialog = null;
function wipePage() {
  for (const id of ["vault-password", "vault-salt", "transfer-block", "app-client-secret", "lock-new", "lock-repeat",
    "lock-change-old", "lock-change-new", "lock-change-repeat", "lock-off-password"]) {
    $(id).value = "";
  }
  $("transfer-code").textContent = "";
  show($("transfer-area"), false);
  show($("vault-result"), false);
  closeSheet();
  if (cancelPasswordDialog) cancelPasswordDialog();
}

async function unlock() {
  const field = $("unlock-password");
  await run($("unlock-result"), async () => {
    setNotice($("unlock-result"), "info", snapshot && snapshot.unlockConnects ?
      "Unlocking and connecting the drive…" : "Unlocking…");
    try {
      await api.Unlock(field.value);
      show($("unlock-result"), false);
    } finally {
      field.value = "";
    }
  });
}
$("btn-unlock").addEventListener("click", unlock);
$("unlock-password").addEventListener("keydown", (e) => { if (e.key === "Enter") unlock(); });

$("btn-reset-app").addEventListener("click", () => run($("unlock-result"), async () => {
  if (!confirm("Reset CryptBridge?\n\nThe pCloud sign-in, the vault keys and the app password are deleted " +
      "from this PC. Your files in pCloud stay untouched.\n\nTo open your vault again you need password and salt " +
      "from your emergency sheet.")) return;
  if (!confirm("Really reset CryptBridge? This cannot be undone.")) return;
  await api.ResetApp();
}));

// askAppPassword asks for the app password before keys are shown or handed
// on. It resolves to the password, to "" without app lock, or to null if
// the user cancels.
function askAppPassword(text) {
  if (!snapshot || !snapshot.appLock) return Promise.resolve("");
  return new Promise((resolve) => {
    const input = $("pw-input");
    const finish = (value) => {
      show($("pw-dialog"), false);
      input.value = "";
      $("pw-ok").onclick = $("pw-cancel").onclick = input.onkeydown = null;
      cancelPasswordDialog = null;
      resolve(value);
    };
    $("pw-text").textContent = text;
    input.value = "";
    $("pw-ok").onclick = () => finish(input.value);
    $("pw-cancel").onclick = () => finish(null);
    input.onkeydown = (e) => {
      if (e.key === "Enter") finish(input.value);
      if (e.key === "Escape") finish(null);
    };
    cancelPasswordDialog = () => finish(null);
    show($("pw-dialog"), true);
    input.focus();
  });
}

function takeValues(...ids) {
  const values = ids.map((id) => $(id).value);
  for (const id of ids) $(id).value = "";
  return values;
}

$("btn-enable-lock").addEventListener("click", () => run($("lock-result"), async () => {
  const [password, again] = takeValues("lock-new", "lock-repeat");
  if (password !== again) throw "The two passwords are not the same.";
  await api.EnableLock(password);
  setNotice($("lock-result"), "good", "The app lock is on. CryptBridge now locks itself with Windows and asks " +
    "for the app password after starting. Keep your emergency sheet: it opens the vault even if you forget the app password.");
}));

$("btn-change-lock").addEventListener("click", () => run($("lock-result"), async () => {
  const [current, password, again] = takeValues("lock-change-old", "lock-change-new", "lock-change-repeat");
  if (password !== again) throw "The two new passwords are not the same.";
  await api.ChangePassword(current, password);
  setNotice($("lock-result"), "good", "The app password is changed.");
}));

$("btn-disable-lock").addEventListener("click", () => run($("lock-result"), async () => {
  const [current] = takeValues("lock-off-password");
  if (!confirm("Turn the app lock off?\n\nCryptBridge then starts without asking and no longer locks itself with Windows.")) return;
  await api.DisableLock(current);
  setNotice($("lock-result"), "info", "The app lock is off.");
}));

$("btn-lock-now").addEventListener("click", () => run($("lock-result"), () => api.Lock()));

$("btn-offer-lock").addEventListener("click", () => {
  openTab("settings");
  $("lock-new").focus();
});
$("btn-decline-lock").addEventListener("click", () => run($("status-message"), () => api.DeclineLock()));

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
  const redirectURL = await api.PCloudRedirectURL();
  document.querySelectorAll(".redirect-url").forEach((el) => { el.textContent = redirectURL; });
  const s = await api.GetSnapshot();
  render(s);
  if (s.phase === "setup") openTab("vault");
}

init();
