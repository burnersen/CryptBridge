// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

"use strict";

// All dynamic text is set with textContent, never innerHTML: folder and file
// names come from pCloud or the disk and must never be able to inject markup.

const $ = (id) => document.getElementById(id);
let api = null;          // window.go.main.App, set once Wails is ready
let snapshot = null;     // last state from the engine
let busy = false;        // an action is running: ignore further clicks
let systemTheme = "dark";

// The page of the Vaults tab: "account", "add" or the ID of a vault.
let currentPage = "account";
// The vault whose form fields were filled last; they are filled again only
// when another vault is shown, so that typing is never overwritten.
let filledVault = "";

// ---------- helpers ----------

function show(el, visible) { el.classList.toggle("hidden", !visible); }

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// icon returns an <svg> showing one of the symbols at the top of the page.
function icon(name, className) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", className);
  const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
  use.setAttribute("href", "#i-" + name);
  svg.append(use);
  return svg;
}

function setIcon(svg, name) {
  svg.firstChild.setAttribute("href", "#i-" + name);
}

function formatBytes(n) {
  if (!n && n !== 0) return "–";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function formatCount(n) { return Number(n || 0).toLocaleString("en-US"); }

function formatWhen(iso) {
  const date = new Date(iso);
  return isNaN(date) ? "" : date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

function elapsed(iso) {
  const seconds = Math.max(0, Math.round((Date.now() - new Date(iso)) / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
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

function setNotice(node, kind, text) {
  node.className = "notice " + kind;
  node.textContent = text;
  show(node, true);
}

function vaultByID(id) {
  return snapshot ? (snapshot.vaults || []).find((v) => v.id === id) : undefined;
}

// isActive: the drive is connected or CryptBridge is trying to connect it,
// so the action offered is "disconnect".
function isActive(v) {
  return ["connected", "reconnecting", "waiting", "connecting"].includes(v.state);
}

const STATES = {
  disconnected: ["Disconnected", ""],
  connecting: ["Connecting…", "warn"],
  connected: ["Connected", "good"],
  reconnecting: ["Reconnecting…", "warn"],
  waiting: ["Waiting for its folder", "warn"],
  error: ["Problem", "bad"],
};

function stateOf(v) { return STATES[v.state] || [v.state, ""]; }

// toggleLabel names what the connect button of a drive does now.
function toggleLabel(v) {
  if (v.state === "waiting") return "Stop waiting";
  return isActive(v) ? "Disconnect" : "Connect";
}

// ---------- theme ----------

function applyTheme() {
  const choice = snapshot ? snapshot.theme : "auto";
  document.documentElement.dataset.theme = choice === "light" || choice === "dark" ? choice : systemTheme;
}

// Windows' own choice is read again whenever the window comes to the front:
// that is when a change made in the Windows settings can be noticed.
async function refreshSystemTheme() {
  if (!api) return;
  try {
    systemTheme = await api.SystemTheme();
  } catch {
    systemTheme = window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  applyTheme();
}
window.addEventListener("focus", refreshSystemTheme);
window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", refreshSystemTheme);

// ---------- tabs and pages ----------

function openTab(name, page) {
  document.querySelectorAll(".tab").forEach((t) => t.classList.toggle("active", t.dataset.tab === name));
  document.querySelectorAll(".tab-page").forEach((p) => p.classList.toggle("active", p.id === "tab-" + name));
  if (name === "settings") loadSettings();
  if (name === "vaults") openPage(page || currentPage);
}

function openPage(page) {
  if (page !== currentPage) {
    show($("vaults-notice"), false);
    leaveVaultPage();
  }
  currentPage = page;
  const isVault = page !== "account" && page !== "add";
  $("page-account").classList.toggle("active", page === "account");
  $("page-add").classList.toggle("active", page === "add");
  $("page-vault").classList.toggle("active", isVault);
  $("side-account").classList.toggle("active", page === "account");
  $("side-add").classList.toggle("active", page === "add");
  document.querySelectorAll("#side-vaults .side-item").forEach((b) => b.classList.toggle("active", b.dataset.vault === page));
  if (page === "add") prepareAdd();
  if (isVault && snapshot) renderVaultPage(snapshot);
  renderCrumbs();
}

// renderCrumbs shows where the user is: "Vaults › Films (Z:)".
function renderCrumbs() {
  const crumbs = $("crumbs");
  crumbs.replaceChildren();
  let here = "pCloud account";
  if (currentPage === "add") here = "Add a vault";
  const v = vaultByID(currentPage);
  if (v) here = `${v.name} (${v.letter}:)`;
  crumbs.append(el("span", "", "Vaults › "), el("strong", "", here));
}

// leaveVaultPage removes what must not stay on screen for another vault:
// the transfer block holds keys.
function leaveVaultPage() {
  filledVault = "";
  $("transfer-block").value = "";
  $("transfer-code").textContent = "";
  show($("transfer-area"), false);
  show($("v-result"), false);
  $("v-save-result").textContent = "";
}

document.querySelectorAll(".tab").forEach((t) => t.addEventListener("click", () => openTab(t.dataset.tab)));
document.addEventListener("click", (e) => {
  const target = e.target.closest("[data-goto], [data-page]");
  if (!target || target.closest("#side-vaults")) return;
  if (target.dataset.goto) openTab(target.dataset.goto, target.dataset.page);
  else openTab("vaults", target.dataset.page);
});
document.querySelectorAll("[data-url]").forEach((b) => b.addEventListener("click", () => api && api.OpenURL(b.dataset.url)));

// ---------- render ----------

function render(s) {
  snapshot = s;
  s.vaults = s.vaults || [];
  applyTheme();
  $("version").textContent = "v" + s.version;
  $("about-version").textContent = s.version;
  renderPill(s);
  renderNotices(s);
  renderCards(s);
  renderSidebar(s);
  if (currentPage !== "account" && currentPage !== "add" && !vaultByID(currentPage)) {
    openPage(s.vaults.length > 0 ? s.vaults[0].id : "add");
  }
  renderVaultPage(s);
  renderCrumbs();
  renderAccount(s);
  renderApp(s);
  renderAddAvailability(s);
  renderLock(s);
  renderLAN(s);
}

function renderPill(s) {
  const pill = $("phase-pill");
  let text = "Starting…";
  let kind = "warn";
  const connected = s.vaults.filter((v) => v.state === "connected").length;
  switch (s.phase) {
    case "setup": text = "No vault yet"; kind = ""; break;
    case "error": text = "Problem"; kind = "bad"; break;
    case "locked": text = "Locked"; kind = ""; break;
    case "ready":
      if (s.vaults.some((v) => v.state === "error")) {
        text = "A drive has a problem"; kind = "bad";
      } else if (connected === 0) {
        text = "Disconnected"; kind = "";
      } else {
        text = s.vaults.length === 1 ? "Connected" : `${connected} of ${s.vaults.length} connected`;
        kind = "good";
      }
  }
  pill.textContent = text;
  pill.className = "pill " + kind;
}

function renderNotices(s) {
  show($("status-message"), !!s.message && s.phase === "error");
  $("status-message").textContent = s.message || "";
  show($("signin-problem"), !!s.signInProblem);
  $("signin-problem-text").textContent = s.signInProblem || "";
  show($("signin-problem-tip"), !s.ownPCloudApp);
  show($("winfsp-missing"), !s.winFsp);
  show($("rclone-missing"), !s.rclone && s.phase === "error");
  show($("setup-needed"), s.phase === "setup");
  renderLANNotice(s);
}

// renderLANNotice tells on the Status page that the drives are open to the
// home network, or why an access does not work although it is on.
function renderLANNotice(s) {
  const notice = $("lan-notice");
  const on = s.locked ? [] : LAN_ROLES.filter((role) => s.lan[role].enabled);
  const failing = on.filter((role) => !s.lan[role].running && s.lan[role].problem);
  const running = on.filter((role) => s.lan[role].running);
  if (failing.length > 0) {
    setNotice(notice, "warn", failing.map((role) => `${LAN_NAMES[role]}: ${s.lan[role].problem}`).join(" "));
  } else if (running.length > 0) {
    const where = running.map((role) => `${LAN_NAMES[role].toLowerCase()} ${s.lan[role].addresses[0] || ""}`.trim());
    setNotice(notice, "info", `Shared in your home network: ${s.lan[running[0]].folders.join(", ")} (${where.join(", ")})`);
  } else {
    show(notice, false);
  }
}

// ---------- status: one card per drive ----------

const cards = new Map(); // by vault ID

function renderCards(s) {
  const container = $("drive-cards");
  for (const [id, card] of cards) {
    if (!vaultByID(id)) {
      card.root.remove();
      cards.delete(id);
    }
  }
  s.vaults.forEach((v, index) => {
    let card = cards.get(v.id);
    if (!card) {
      card = createCard(v.id);
      cards.set(v.id, card);
    }
    // Move a card only when the order changed: moving it under the mouse
    // could swallow a click.
    if (container.children[index] !== card.root) container.insertBefore(card.root, container.children[index] || null);
    updateCard(card, v, s);
  });
  const connected = s.vaults.filter(isActive).length;
  show($("summary"), s.vaults.length > 1 && !s.locked);
  $("summary-text").textContent = `${s.vaults.length} vaults · ${s.vaults.filter((v) => v.state === "connected").length} connected`;
  $("btn-connect-all").disabled = connected === s.vaults.length;
  $("btn-disconnect-all").disabled = connected === 0;
}

function createCard(id) {
  const c = {};
  c.root = el("div", "card drive-card");
  c.letter = el("div", "drive-letter");
  const main = el("div", "drive-main");
  const head = el("div", "drive-head");
  c.kind = icon("cloud", "kind-icon");
  c.name = el("strong");
  c.badge = el("span", "badge");
  head.append(c.kind, c.name, c.badge);
  c.where = el("div", "muted small drive-where");
  c.space = el("div", "drive-line");
  c.bar = el("div", "bar");
  c.fill = el("div", "bar-fill");
  c.bar.append(c.fill);
  c.spaceText = el("div", "muted small");
  c.space.append(c.bar, c.spaceText);
  c.size = el("div", "drive-line small muted");
  c.uploads = el("div", "drive-line small");
  c.transfers = el("ul", "transfers");
  c.message = el("div", "drive-message");
  main.append(head, c.where, c.space, c.size, c.uploads, c.transfers, c.message);
  const actions = el("div", "drive-actions");
  c.toggle = el("button", "primary", "Connect");
  c.open = el("button", "", "Open drive");
  c.details = el("button", "link", "Details");
  actions.append(c.toggle, c.open, c.details);
  c.root.append(c.letter, main, actions);

  c.toggle.addEventListener("click", () => run($("status-message"), () => toggleVault(id)));
  c.open.addEventListener("click", () => run($("status-message"), () => api.OpenDrive(id)));
  c.details.addEventListener("click", () => openTab("vaults", id));
  return c;
}

function updateCard(c, v, s) {
  const [label, kind] = stateOf(v);
  const active = isActive(v);
  c.root.classList.toggle("off", !active);
  c.letter.textContent = v.letter + ":";
  c.letter.className = "drive-letter" + (v.state === "connected" ? " on" : kind === "warn" ? " warn" : kind === "bad" ? " bad" : "");
  setIcon(c.kind, v.kind === "local" ? "disk" : "cloud");
  c.name.textContent = v.name;
  c.badge.textContent = label;
  c.badge.className = "badge " + kind;
  c.where.textContent = v.kind === "local" ? v.location : "pCloud · " + (v.location || "");

  const space = spaceOf(v, s);
  show(c.space, !!space);
  if (space) {
    c.fill.style.width = space.percent.toFixed(1) + "%";
    c.fill.classList.toggle("full", space.percent > 90);
    c.spaceText.textContent = space.text;
  }
  const size = sizeText(v);
  show(c.size, !!size);
  c.size.textContent = size;

  const d = v.drive || {};
  const pending = (d.uploadsInProgress || 0) + (d.uploadsQueued || 0);
  show(c.uploads, pending > 0);
  c.uploads.textContent = `↑ ${d.uploadsInProgress || 0} being written, ${d.uploadsQueued || 0} waiting` +
    (v.kind === "pcloud" ? " — keep the drive connected until they are in pCloud." : ".");
  renderTransfers(c.transfers, d.transfers || []);

  show(c.message, !!v.message);
  c.message.textContent = v.message || "";
  c.message.className = "drive-message " + kind;

  c.toggle.textContent = toggleLabel(v);
  c.toggle.className = active ? "" : "primary";
  c.toggle.disabled = !v.hasKeys || v.state === "connecting" || (!active && v.kind === "pcloud" && !s.signedIn);
  c.open.disabled = !d.mounted;
}

// spaceOf describes where the vault lives: the pCloud account, or the disk
// of a local vault.
function spaceOf(v, s) {
  if (v.kind === "pcloud" && s.usage && s.usage.total > 0) {
    return {
      percent: s.usage.used / s.usage.total * 100,
      text: `pCloud: ${formatBytes(s.usage.used)} used, ${formatBytes(s.usage.free)} free of ${formatBytes(s.usage.total)}`,
    };
  }
  if (v.kind === "local" && v.space && v.space.total > 0) {
    const drive = (v.location || "").slice(0, 2);
    return {
      percent: v.space.used / v.space.total * 100,
      text: `Drive ${drive} ${formatBytes(v.space.used)} used, ${formatBytes(v.space.free)} free of ${formatBytes(v.space.total)}`,
    };
  }
  return null;
}

function sizeText(v) {
  if (v.counting) return `Counting the vault… ${formatCount(v.counting.listed)} entries so far (${elapsed(v.counting.started)})`;
  if (v.size) return `Vault: ${formatBytes(v.size.bytes)} in ${formatCount(v.size.files)} files (counted ${formatWhen(v.size.at)})`;
  return "";
}

function renderTransfers(list, transfers) {
  list.replaceChildren();
  for (const t of transfers) {
    const li = el("li");
    const head = el("div", "tname");
    head.append(el("span", "", t.name), el("span", "", `${t.percentage}% · ${formatBytes(t.speed)}/s`));
    const bar = el("div", "bar");
    const fill = el("div", "bar-fill");
    fill.style.width = t.percentage + "%";
    bar.append(fill);
    li.append(head, bar);
    list.append(li);
  }
}

// ---------- connecting and disconnecting ----------

function uploadQuestion(pending) {
  return confirm(`${pending} file(s) are still being written into the vault.\n\n` +
    "If you disconnect now, they stay on this PC and are written into the vault the next time you connect.\n\n" +
    "Disconnect anyway?");
}

// disconnectVault removes a drive, asking first while uploads are running.
// It returns false if the user chose to keep the drive.
async function disconnectVault(id) {
  const pending = await api.Disconnect(id, false);
  if (pending === 0) return true;
  const ok = uploadQuestion(pending);
  if (ok) await api.Disconnect(id, true);
  return ok;
}

async function toggleVault(id) {
  const v = vaultByID(id);
  if (!v) return;
  if (isActive(v)) {
    await disconnectVault(id);
  } else {
    await api.Connect(id);
  }
}

$("btn-connect-all").addEventListener("click", () => run($("status-message"), () => api.ConnectAll()));
$("btn-disconnect-all").addEventListener("click", () => run($("status-message"), async () => {
  const pending = await api.DisconnectAll(false);
  if (pending > 0 && uploadQuestion(pending)) await api.DisconnectAll(true);
}));
$("winfsp-download").addEventListener("click", () => api.OpenWinFspDownload());

// ---------- sidebar ----------

const sideItems = new Map(); // by vault ID

function renderSidebar(s) {
  const dot = $("side-account-dot");
  dot.className = "dot" + (s.signInProblem ? " bad" : s.signedIn ? " good" : "");

  const container = $("side-vaults");
  for (const [id, item] of sideItems) {
    if (!vaultByID(id)) {
      item.root.remove();
      sideItems.delete(id);
    }
  }
  s.vaults.forEach((v, index) => {
    let item = sideItems.get(v.id);
    if (!item) {
      item = { root: el("button", "side-item"), kind: icon("cloud", "side-icon"), name: el("span", "side-name"),
        letter: el("span", "side-letter"), dot: el("span", "dot") };
      item.root.dataset.vault = v.id;
      item.root.append(item.kind, item.name, item.letter, item.dot);
      item.root.addEventListener("click", () => openPage(v.id));
      sideItems.set(v.id, item);
    }
    if (container.children[index] !== item.root) container.insertBefore(item.root, container.children[index] || null);
    setIcon(item.kind, v.kind === "local" ? "disk" : "cloud");
    item.name.textContent = v.name;
    item.letter.textContent = v.letter + ":";
    const [, kind] = stateOf(v);
    item.dot.className = "dot " + kind;
    item.root.title = `${v.name} (${v.letter}:) — ${stateOf(v)[0]}`;
    item.root.classList.toggle("active", currentPage === v.id);
  });
}

// ---------- the page of one vault ----------

function renderVaultPage(s) {
  const v = vaultByID(currentPage);
  if (!v) return;
  const [label, kind] = stateOf(v);
  const active = isActive(v);
  const letter = $("v-letter");
  letter.textContent = v.letter + ":";
  letter.className = "drive-letter" + (v.state === "connected" ? " on" : kind === "warn" ? " warn" : kind === "bad" ? " bad" : "");
  $("v-name").textContent = v.name;
  $("v-where").textContent = v.kind === "local" ? "On this PC · " + v.location : "In pCloud · " + v.location;
  $("v-state").textContent = label;
  $("v-state").className = "badge " + kind;

  const connect = $("btn-v-connect");
  connect.textContent = toggleLabel(v);
  connect.className = active ? "" : "primary";
  connect.disabled = !v.hasKeys || v.state === "connecting" || (!active && v.kind === "pcloud" && !s.signedIn);
  $("btn-v-open").disabled = !(v.drive && v.drive.mounted);

  show($("v-other-folder"), !!v.otherFolder);
  $("v-other-text").textContent = v.otherFolder ? v.message : "";
  if (v.message && !v.otherFolder) {
    setNotice($("v-message"), v.state === "error" ? "error" : v.state === "waiting" ? "warn" : "info", v.message);
  } else {
    show($("v-message"), false);
  }

  $("v-location-label").textContent = v.kind === "local" ? "Folder on this PC" : "Folder in pCloud";
  $("v-location").textContent = v.location || "–";
  $("v-fingerprint").textContent = v.fingerprint || "–";
  $("v-size").textContent = v.counting ?
    `Counting… ${formatCount(v.counting.listed)} entries so far (${elapsed(v.counting.started)})` :
    v.size ? `${formatBytes(v.size.bytes)} in ${formatCount(v.size.files)} files` : "not counted yet";
  $("v-size-hint").textContent = (v.size ? `Counted ${formatWhen(v.size.at)}. ` : "") + (v.kind === "local" ?
    "Counted by itself every few minutes while the drive is connected." :
    "Counting lists every file in pCloud — for terabytes this takes a few minutes.");
  if (v.sizeError) $("v-size-hint").textContent = v.sizeError;
  show($("btn-v-count"), !v.counting);
  show($("btn-v-count-cancel"), !!v.counting);
  $("btn-v-count").disabled = v.kind === "pcloud" && !s.signedIn;
  $("v-fingerprint-hint").textContent = v.kind === "local" ?
    "The check code stays the same whatever drive letter the disk gets. If it ever differs, the keys differ." :
    "The check code must be the same on your Linux server. If it differs, the keys differ.";
  $("v-edit-auto-label").textContent = v.kind === "local" ?
    "Connect when CryptBridge starts and whenever its folder appears (USB drive plugged in)" :
    "Connect when CryptBridge starts";
  show($("v-linux"), v.kind === "pcloud");

  if (filledVault !== v.id) fillVaultForm(v);
}

async function fillVaultForm(v) {
  filledVault = v.id;
  $("v-edit-name").value = v.name;
  $("v-edit-auto").checked = v.autoConnect;
  await fillLetters($("v-edit-letter"), v.id, v.letter);
}

async function fillLetters(select, id, wanted) {
  const letters = await api.FreeLetters(id);
  select.replaceChildren();
  for (const l of letters) {
    const option = el("option", "", l + ":");
    option.value = l;
    select.append(option);
  }
  if (wanted && letters.includes(wanted)) select.value = wanted;
  else if (letters.includes("Z")) select.value = "Z";
  else if (letters.length > 0) select.value = letters[letters.length - 1];
}

$("btn-v-connect").addEventListener("click", () => run($("v-result"), () => toggleVault(currentPage)));
$("btn-v-open").addEventListener("click", () => run($("v-result"), () => api.OpenDrive(currentPage)));
$("btn-v-use-folder").addEventListener("click", () => run($("v-result"), async () => {
  const v = vaultByID(currentPage);
  if (!v || !confirm(`Work with the vault in ${v.location} from now on?\n\n` +
      "Only do this if it is the vault you want to change — for example after moving the vault to a new disk. " +
      "If it is an old copy, changes made there are missing in the other one.")) return;
  await api.UseFolder(v.id);
}));
$("btn-v-count").addEventListener("click", () => run($("v-result"), () => api.CountSize(currentPage)));
$("btn-v-count-cancel").addEventListener("click", () => run($("v-result"), () => api.CancelCount(currentPage)));
$("btn-v-save").addEventListener("click", () => run($("v-result"), async () => {
  const id = currentPage;
  $("v-save-result").textContent = "";
  await api.UpdateVault(id, {
    name: $("v-edit-name").value,
    letter: $("v-edit-letter").value,
    autoConnect: $("v-edit-auto").checked,
  });
  render(await api.GetSnapshot()); // the snapshot event may arrive only after this
  const v = vaultByID(id);
  if (v) await fillVaultForm(v);
  $("v-save-result").textContent = "Saved.";
}));

// ---------- removing a vault ----------

let removing = "";

$("btn-v-remove").addEventListener("click", () => {
  const v = vaultByID(currentPage);
  if (!v) return;
  removing = v.id;
  $("remove-name").textContent = v.name;
  $("remove-name-2").textContent = v.name;
  $("remove-confirm").value = "";
  $("btn-remove-ok").disabled = true;
  show($("remove-result"), false);
  show($("remove-dialog"), true);
  $("remove-confirm").focus();
});
$("remove-confirm").addEventListener("input", () => {
  const v = vaultByID(removing);
  $("btn-remove-ok").disabled = !v || $("remove-confirm").value.trim().toLowerCase() !== v.name.toLowerCase();
});
function closeRemoveDialog() {
  removing = "";
  $("remove-confirm").value = "";
  show($("remove-dialog"), false);
}
$("btn-remove-cancel").addEventListener("click", closeRemoveDialog);
$("btn-remove-sheet").addEventListener("click", () => run($("remove-result"), () => showSheet(removing)));
$("btn-remove-ok").addEventListener("click", () => run($("remove-result"), async () => {
  const v = vaultByID(removing);
  await api.RemoveVault(removing, $("remove-confirm").value);
  closeRemoveDialog();
  render(await api.GetSnapshot());
  openPage(snapshot.vaults.length > 0 ? snapshot.vaults[0].id : "add");
  setNotice($("vaults-notice"), "good", `${v ? v.name : "The vault"} was removed from CryptBridge. Its files are untouched.`);
}));

// ---------- pCloud account ----------

function renderAccount(s) {
  const region = s.pcloudHost === "eapi.pcloud.com" ? "EU" : (s.pcloudHost ? "US" : "");
  const via = s.signInAppId ? "your own pCloud app" : "rclone's shared access";
  $("account-state").textContent = s.signedIn ? `Signed in (pCloud ${region} region) through ${via}.` : "Not signed in.";
  $("btn-signin").textContent = s.signedIn ? "Sign in again" : "Sign in to pCloud";
  show($("btn-signout"), s.signedIn);
  show($("signin-hint-shared"), !s.ownPCloudApp);
  show($("signin-hint-own"), s.ownPCloudApp);
  const usage = s.signedIn && s.usage && s.usage.total > 0;
  show($("account-usage"), usage);
  if (usage) {
    $("account-usage-fill").style.width = (s.usage.used / s.usage.total * 100).toFixed(1) + "%";
    $("account-usage-text").textContent = `${formatBytes(s.usage.used)} used, ${formatBytes(s.usage.free)} free of ${formatBytes(s.usage.total)}`;
  }
}

// pcloudDrivesOff disconnects the connected pCloud drives (asking about
// uploads) and returns their IDs, or null if the user kept a drive.
async function pcloudDrivesOff() {
  const ids = [];
  for (const v of snapshot.vaults) {
    if (v.kind !== "pcloud" || !isActive(v)) continue;
    if (!(await disconnectVault(v.id))) return null;
    ids.push(v.id);
  }
  return ids;
}

// signIn runs the pCloud sign-in in the browser. A mounted drive keeps
// using the old sign-in, so connected pCloud drives are disconnected first
// and connected again afterwards — also when the sign-in fails or is
// cancelled, so that nothing is left worse than before.
async function signIn() {
  openTab("vaults", "account");
  const reconnect = await pcloudDrivesOff();
  if (reconnect === null) return;

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
  for (const id of reconnect) {
    try {
      await api.Connect(id);
    } catch (err) {
      failure = failure || err;
    }
  }
  if (failure) throw failure;
  setNotice($("account-result"), "good", reconnect.length > 0 ?
    "Signed in to pCloud. The pCloud drives are connected again." : "Signed in to pCloud.");
}

let signInLink = "";
$("btn-signin").addEventListener("click", () => run($("account-result"), signIn));
$("btn-resignin").addEventListener("click", () => run($("account-result"), signIn));
$("btn-signin-cancel").addEventListener("click", () => api.CancelSignIn());
$("signin-link").addEventListener("click", () => signInLink && api.OpenURL(signInLink));
$("btn-signout").addEventListener("click", () => run($("account-result"), async () => {
  if (!confirm("Sign out of pCloud?\n\nThe pCloud drives are disconnected first. The vault keys are kept.")) return;
  if ((await pcloudDrivesOff()) === null) return;
  await api.SignOut();
  setNotice($("account-result"), "info", "Signed out of pCloud.");
}));

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
  render(await api.GetSnapshot());
  loadAppFields();
  if (!snapshot.signedIn) {
    setNotice($("app-result"), "good", "Saved. Your app is used when you sign in above.");
    return;
  }
  if (confirm("Your pCloud app is saved.\n\nSign in again now, so that CryptBridge uses it? " +
      "Connected pCloud drives are disconnected for the sign-in and connected again afterwards.")) {
    await signIn();
    return;
  }
  setNotice($("app-result"), "good", "Saved. CryptBridge uses your app from the next sign-in (Sign in again, above).");
}));

$("btn-remove-app").addEventListener("click", () => run($("app-result"), async () => {
  if (!confirm("Use rclone's shared access again instead of your own pCloud app?\n\n" +
      "Your current sign-in keeps working; the shared access is used from the next sign-in.")) return;
  await api.SetPCloudApp("", "");
  render(await api.GetSnapshot());
  loadAppFields();
  setNotice($("app-result"), "info", "Removed. The next sign-in uses rclone's shared access.");
}));

// copyWithFeedback copies a text and lets the button say so for a moment.
async function copyWithFeedback(button, text) {
  await api.CopyText(text);
  const label = button.dataset.label || (button.dataset.label = button.textContent);
  button.textContent = "Copied";
  setTimeout(() => { button.textContent = label; }, 1500);
}

// The page is not selectable, so texts to paste into pCloud's form have a
// Copy button. Line breaks of the HTML source are not part of the text.
document.querySelectorAll("[data-copy]").forEach((b) => b.addEventListener("click", () =>
  copyWithFeedback(b, $(b.dataset.copy).textContent.replace(/\s+/g, " ").trim())));

document.querySelectorAll("[data-reveal]").forEach((b) => b.addEventListener("click", () => {
  const input = $(b.dataset.reveal);
  input.type = input.type === "password" ? "text" : "password";
}));
document.querySelectorAll("[data-generate]").forEach((b) => b.addEventListener("click", async () => {
  const input = $(b.dataset.generate);
  input.value = await api.NewPassword();
  input.type = "text"; // the user has to see it to write it down
}));

// ---------- adding a vault ----------

let addKind = "";       // "pcloud" or "local"
let autoName = "";      // the name CryptBridge suggested, replaced when the folder changes
let candidates = [];    // vaults found in pCloud by the search

function addMode() {
  return document.querySelector("input[name=add-mode]:checked").value;
}

function renderAddAvailability(s) {
  show($("add-signin-needed"), addKind === "pcloud" && !s.signedIn);
  show($("add-form"), addKind === "local" || (addKind === "pcloud" && s.signedIn));
}

async function prepareAdd() {
  renderAddAvailability(snapshot || {});
  applyAddMode();
  if (addKind) await fillLetters($("add-letter"), "", $("add-letter").value);
}

function setAddKind(kind) {
  if (kind === addKind) return;
  addKind = kind;
  document.querySelectorAll(".choice").forEach((c) => c.classList.toggle("active", c.dataset.kind === kind));
  $("add-folder").value = kind === "pcloud" ? "CryptBridge" : "";
  $("add-folder-label").textContent = kind === "pcloud" ? "Folder in pCloud" : "Folder on this PC or a USB drive";
  $("btn-add-browse").textContent = kind === "pcloud" ? "Browse pCloud…" : "Choose folder…";
  show($("add-folder-info"), false);
  show($("candidates"), false);
  show($("add-result"), false);
  if ($("add-name").value === autoName) {
    $("add-name").value = "";
    autoName = "";
  }
  suggestName();
  prepareAdd();
}

document.querySelectorAll(".choice").forEach((c) => c.addEventListener("click", () => setAddKind(c.dataset.kind)));

function applyAddMode() {
  const isNew = addMode() === "new";
  document.querySelectorAll("#page-add .only-new, #browser .only-new").forEach((node) => show(node, isNew));
  document.querySelectorAll("#page-add .only-existing").forEach((node) => {
    show(node, !isNew && (!node.classList.contains("only-pcloud") || addKind === "pcloud"));
  });
  show($("add-result"), false);
}
document.querySelectorAll("input[name=add-mode]").forEach((r) => r.addEventListener("change", () => {
  applyAddMode();
  inspectAddFolder();
}));

// suggestName names the vault after its folder, unless the user typed a name.
function suggestName() {
  const field = $("add-name");
  if (field.value !== "" && field.value !== autoName) return;
  const parts = $("add-folder").value.split(/[\\/]/).filter((p) => p && !p.endsWith(":"));
  autoName = (parts.pop() || "").replace(/[\\/:*?"<>|]/g, "").slice(0, 32);
  field.value = autoName;
}

const FOLDER_TEXTS = {
  missing: { new: ["good", "This folder does not exist yet — CryptBridge creates it."], existing: ["bad", "There is no folder here."] },
  vault: { new: ["bad", "This folder holds a vault. For a new vault choose an empty folder — or open this one as an existing vault."],
    existing: ["good", "Looks like a vault. Enter its password and salt."] },
  empty: { new: ["good", "Empty folder — a new vault can be created here."], existing: ["bad", "This folder is empty: there is no vault to open."] },
  plain: { new: ["bad", "This folder holds normal files. A new vault needs an empty folder."],
    existing: ["warn", "This folder does not look like a vault — its names are not encrypted."] },
};

let inspectRun = 0;
async function inspectAddFolder() {
  const path = $("add-folder").value.trim();
  const info = $("add-folder-info");
  if (!addKind || path === "") {
    show(info, false);
    return;
  }
  const mine = ++inspectRun;
  try {
    const folder = await api.InspectFolder(addKind, path);
    if (mine !== inspectRun) return; // a newer look is on its way
    const kind = !folder.exists ? "missing" : folder.looksLikeVault ? "vault" : folder.entries === 0 ? "empty" : "plain";
    const [tone, text] = FOLDER_TEXTS[kind][addMode() === "new" ? "new" : "existing"];
    info.className = "folder-info " + tone;
    info.textContent = folder.entries > 0 ? `${text} (${formatCount(folder.entries)} entries)` : text;
  } catch (err) {
    if (mine !== inspectRun) return;
    info.className = "folder-info bad";
    info.textContent = errorText(err);
  }
  show(info, true);
}

$("add-folder").addEventListener("change", () => { suggestName(); inspectAddFolder(); });

$("btn-add-browse").addEventListener("click", () => run($("add-result"), async () => {
  if (addKind === "pcloud") {
    const path = $("add-folder").value.trim();
    await openBrowser(path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : "");
    return;
  }
  const folder = await api.ChooseVaultFolder($("add-folder").value.trim());
  if (!folder) return;
  $("add-folder").value = folder;
  suggestName();
  await inspectAddFolder();
}));

$("btn-find").addEventListener("click", () => run($("add-result"), async () => {
  setNotice($("add-result"), "info", "Searching pCloud for vaults… (only names are read, nothing is downloaded)");
  candidates = (await api.FindVaults()) || [];
  const list = $("candidates");
  list.replaceChildren();
  for (const c of candidates) {
    const li = el("li");
    li.append(icon("lock", "side-icon"), el("span", "", `${c.path}  (${c.entries} entries${c.confirmed ? ", confirmed rclone vault" : ""})`));
    li.addEventListener("click", () => {
      $("add-folder").value = c.path;
      show(list, false);
      suggestName();
      inspectAddFolder();
    });
    list.append(li);
  }
  show(list, candidates.length > 0);
  setNotice($("add-result"), "info", candidates.length === 0 ?
    "No vault found in your pCloud." : "Click a folder to use it, then enter password and salt.");
}));

function addRequest(acceptUnverifiable) {
  const isNew = addMode() === "new";
  return {
    kind: addKind,
    name: $("add-name").value,
    letter: $("add-letter").value,
    autoConnect: $("add-auto").checked,
    create: isNew,
    acceptUnverifiable: acceptUnverifiable,
    config: {
      path: $("add-folder").value.trim(),
      filenameEncryption: isNew ? "standard" : $("adv-encryption").value,
      directoryNameEncryption: isNew ? true : $("adv-dirnames").checked,
      filenameEncoding: isNew ? "base32" : $("adv-encoding").value,
      suffix: isNew ? ".bin" : ($("adv-suffix").value || ".bin"),
      password: $("add-password").value,
      password2: $("add-salt").value,
    },
  };
}

const VERDICTS = {
  empty: ["info", "The folder is empty (or does not exist yet): a new vault can be created here."],
  match: ["good", "The keys open this vault."],
  mismatch: ["error", "These keys do NOT open the vault in this folder. Check password, salt and the advanced settings."],
  unverifiable: ["warn", "This vault does not encrypt file names, so the keys cannot be checked."],
};

function checkText(check) {
  const [kind, text] = VERDICTS[check.verification.verdict] || ["info", check.verification.verdict];
  return [kind, text + (check.fingerprint ? `  Check code: ${check.fingerprint}` : "")];
}

$("btn-check").addEventListener("click", () => run($("add-result"), async () => {
  setNotice($("add-result"), "info", "Checking…");
  const [kind, text] = checkText(await api.CheckVault(addRequest(false)));
  setNotice($("add-result"), kind, text);
}));

$("btn-add").addEventListener("click", () => run($("add-result"), async () => {
  const create = addMode() === "new";
  if (create && !confirm("Have you written down the password and the salt?\n\nWithout them the vault can never be opened again.")) return;
  setNotice($("add-result"), "info", create ? "Creating the vault…" : "Checking and opening the vault…");
  let check;
  try {
    check = await api.AddVault(addRequest(false));
  } catch (err) {
    const text = errorText(err);
    if (!text.includes("cannot be checked") ||
        !confirm(text + "\n\nUse it anyway? Make sure the keys are right — wrong keys would split the vault.")) throw err;
    check = await api.AddVault(addRequest(true));
  }
  for (const id of ["add-password", "add-salt"]) {
    $(id).value = "";
    $(id).type = "password";
  }
  show($("add-result"), false);
  show($("add-folder-info"), false);
  render(await api.GetSnapshot());
  openPage(check.vaultId);
  const v = vaultByID(check.vaultId);
  let connected = "";
  try {
    await api.Connect(check.vaultId);
    connected = ` It is connected as ${v.letter}:.`;
  } catch (err) {
    connected = " It could not be connected yet: " + errorText(err);
  }
  showAddedNotice(check.vaultId, (create ? "The vault was created." : "The vault was added.") + connected);
}));

// showAddedNotice tells that a vault was added and offers its emergency
// sheet right away: it is the only way back if the keys are ever lost.
function showAddedNotice(id, text) {
  const notice = $("vaults-notice");
  notice.className = "notice good";
  notice.replaceChildren(el("div", "", text + " Print its emergency sheet now and keep it safe."));
  const row = el("div", "row");
  const button = el("button", "primary", "Show emergency sheet");
  button.addEventListener("click", () => run($("v-result"), () => showSheet(id)));
  row.append(button);
  notice.append(row);
  show(notice, true);
}

// ---------- folder browser for pCloud ----------

let browserPath = "";

async function openBrowser(path) {
  show($("browser"), true);
  $("browser-new-name").value = "";
  applyAddMode();
  await browseTo(path);
}

async function browseTo(path) {
  browserPath = path;
  renderBrowserCrumbs(path);
  const list = $("browser-list");
  list.replaceChildren(el("li", "taken muted", "Loading…"));
  show($("browser-note"), false);
  let folder;
  try {
    folder = await api.InspectFolder("pcloud", path);
  } catch (err) {
    list.replaceChildren();
    setNotice($("browser-note"), "error", errorText(err));
    return;
  }
  if (path !== browserPath) return; // the user went on meanwhile
  list.replaceChildren();
  const registered = vaultAt(path);
  if (registered) {
    setNotice($("browser-note"), "info", `This is the folder of your vault “${registered.name}”.`);
  } else if (folder.looksLikeVault) {
    setNotice($("browser-note"), "info", `This folder is a vault (${formatCount(folder.entries)} entries).`);
  }
  $("btn-browser-choose").disabled = path === "" || !!registered;
  if (folder.looksLikeVault) return; // the folders inside have encrypted names
  for (const name of folder.subfolders) {
    const sub = path === "" ? name : path + "/" + name;
    const li = el("li");
    const known = vaultAt(sub);
    const found = candidates.some((c) => c.path.toLowerCase() === sub.toLowerCase());
    li.append(icon(known || found ? "lock" : "folder", "side-icon"), el("span", "", name));
    if (known) li.append(el("span", "muted small", `— your vault “${known.name}”`));
    else if (found) li.append(el("span", "muted small", "— a vault"));
    li.addEventListener("click", () => browseTo(sub));
    list.append(li);
  }
  if (folder.subfolders.length === 0) list.append(el("li", "taken muted", "No folders here."));
}

function vaultAt(path) {
  return snapshot.vaults.find((v) => v.kind === "pcloud" && v.location.toLowerCase() === path.toLowerCase());
}

function renderBrowserCrumbs(path) {
  const crumbs = $("browser-crumbs");
  crumbs.replaceChildren();
  const top = el("button", "link", "pCloud");
  top.addEventListener("click", () => browseTo(""));
  crumbs.append(top);
  let sofar = "";
  for (const part of path.split("/").filter(Boolean)) {
    sofar = sofar === "" ? part : sofar + "/" + part;
    const target = sofar;
    const button = el("button", "link", part);
    button.addEventListener("click", () => browseTo(target));
    crumbs.append(el("span", "", " / "), button);
  }
}

function closeBrowser(path) {
  show($("browser"), false);
  if (path === undefined) return;
  $("add-folder").value = path;
  suggestName();
  inspectAddFolder();
}

$("btn-browser-choose").addEventListener("click", () => closeBrowser(browserPath));
$("btn-browser-cancel").addEventListener("click", () => closeBrowser());
$("btn-browser-new").addEventListener("click", () => {
  const name = $("browser-new-name").value.trim().replace(/[\\/]/g, "");
  if (name) closeBrowser(browserPath === "" ? name : browserPath + "/" + name);
});

document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  if (!$("browser").classList.contains("hidden")) closeBrowser();
  if (!$("remove-dialog").classList.contains("hidden")) closeRemoveDialog();
});

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

$("btn-transfer").addEventListener("click", () => run($("v-result"), async () => {
  const id = currentPage;
  const password = await askAppPassword("The transfer block hands your keys on. Please confirm with your app password.");
  if (password === null) return;
  const t = await api.CreateTransfer(id, password);
  if (currentPage !== id) return;
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
  const keys = `filename_encryption=${d.filenameEncryption} directory_name_encryption=${d.directoryNameEncryption} ` +
    `filename_encoding=${d.filenameEncoding} suffix=${d.suffix} password="${d.password}"` +
    (d.salt ? ` password2="${d.salt}"` : "") + " --obscure";
  const lines = d.kind === "local" ? [
    `rclone config create cbvault crypt remote="${d.vaultPath}" ${keys}`,
  ] : [
    `rclone config create cbpcloud pcloud hostname=${d.pcloudHost}`,
    "    (a browser opens: sign in to pCloud)",
    `rclone config create cbvault crypt remote="cbpcloud:${d.vaultPath}" ${keys}`,
  ];
  lines.push("    (keep --obscure: without it rclone may store the password wrongly)", "rclone ls cbvault:");
  return lines.join("\n");
}

async function showSheet(id) {
  const password = await askAppPassword("The emergency sheet shows the vault keys. Please confirm with your app password.");
  if (password === null) return;
  const d = await api.EmergencySheet(id, password);
  const local = d.kind === "local";
  $("s-name").textContent = d.name;
  $("s-warning").textContent = local ?
    "Keep this sheet like a house key. Anyone who has it — and the vault folder or a copy of it — can read your vault. " +
      "Without the password and the salt, nobody can open the vault." :
    "Keep this sheet like a house key. Anyone who has it — and access to your pCloud — can read your vault. " +
      "Without the password and the salt, nobody can open the vault, not even pCloud.";
  $("s-created").textContent = d.createdAt;
  document.querySelectorAll(".s-pcloud").forEach((row) => show(row, !local));
  $("s-region").textContent = local ? "" : `${d.region} (${d.pcloudHost})`;
  $("s-path-label").textContent = local ? "Vault folder (on this PC or a USB drive)" : "Vault folder in pCloud";
  $("s-path").textContent = d.vaultPath;
  $("s-password").textContent = d.password;
  $("s-salt").textContent = d.salt || "(none)";
  $("s-encryption").textContent = d.filenameEncryption;
  $("s-dirnames").textContent = d.directoryNameEncryption ? "yes" : "no";
  $("s-encoding").textContent = d.filenameEncoding;
  $("s-suffix").textContent = d.suffix;
  $("s-fingerprint").textContent = d.fingerprint || "–";
  $("s-with-cryptbridge").replaceChildren(el("strong", "", "With CryptBridge: "), document.createTextNode(local ?
    "Add vault → On this PC or a USB drive → Open an existing vault, choose the folder, enter password and salt. " +
      "The check code must match the one above." :
    "sign in to pCloud, Add vault → In pCloud → Open an existing vault, enter folder, password and salt " +
      "(on the Linux server: the setup asks for them). The check code must match the one above."));
  $("s-rclone").textContent = rcloneCommands(d);
  $("s-backup").textContent = local ?
    `Back up this vault like any folder: copy the whole folder ${d.vaultPath} to another disk. The copy stays encrypted.` : "";
  show($("sheet"), true);
}

$("btn-sheet").addEventListener("click", () => run($("v-result"), () => showSheet(currentPage)));

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
  for (const radio of document.querySelectorAll("input[name=theme]")) radio.checked = radio.value === snapshot.theme;
  $("opt-cache-dir").value = snapshot.cacheDir;
  $("opt-cache-max").value = snapshot.cacheMaxSize;
  $("opt-cache-free").value = snapshot.cacheMinFreeSpace;
  $("opt-autoreconnect").checked = snapshot.autoReconnect;
  $("opt-autostart").checked = await api.AutostartEnabled();
  $("settings-result").textContent = "";
  loadAppFields();
  show($("lock-result"), false);
  loadLANFields();
  checkFirewall(); // takes a few seconds; the page shows "Checking…" meanwhile
}

$("btn-browse-cache").addEventListener("click", async () => {
  const folder = await api.ChooseFolder($("opt-cache-dir").value);
  if (folder) $("opt-cache-dir").value = folder;
});

// The theme is saved as soon as it is chosen, with the other options as
// they are stored (not as they may be half-typed).
document.querySelectorAll("input[name=theme]").forEach((radio) => radio.addEventListener("change", () => run(null, async () => {
  const result = $("settings-result");
  try {
    await api.UpdateOptions({
      cacheDir: snapshot.cacheDir,
      cacheMaxSize: snapshot.cacheMaxSize,
      cacheMinFreeSpace: snapshot.cacheMinFreeSpace,
      autoReconnect: snapshot.autoReconnect,
      theme: radio.value,
    });
    render(await api.GetSnapshot());
  } catch (err) {
    result.textContent = errorText(err);
  }
})));

$("btn-save-settings").addEventListener("click", () => run(null, async () => {
  const result = $("settings-result");
  try {
    await api.UpdateOptions({
      cacheDir: $("opt-cache-dir").value.trim(),
      cacheMaxSize: $("opt-cache-max").value.trim(),
      cacheMinFreeSpace: $("opt-cache-free").value.trim(),
      autoReconnect: $("opt-autoreconnect").checked,
      theme: snapshot.theme,
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

  const letters = (s.unlockConnects || []).map((l) => l + ":");
  $("lock-hint").textContent = letters.length === 0 ? "Enter your app password to open your vaults." :
    `Enter your app password to open your vaults. ${letters.length === 1 ? "The drive" : "The drives"} ` +
      `${letters.join(", ")} ${letters.length === 1 ? "connects" : "connect"} afterwards.`;
  show($("lock-status"), s.locked && !!s.message);
  $("lock-status").textContent = s.locked ? s.message || "" : "";

  show($("lock-offer"), !s.appLock && !s.lockOffered && s.vaults.length > 0 && !s.locked);
  $("lock-state").textContent = s.appLock ?
    "On — CryptBridge locks itself with Windows and asks for the app password after starting." : "Off";
  show($("lock-off"), !s.appLock);
  show($("lock-on"), s.appLock);
}

// wipePage removes everything from the page that could show keys: the lock
// screen covers the window, but the page itself must not keep them either.
let cancelPasswordDialog = null;
function wipePage() {
  for (const id of ["add-password", "add-salt", "transfer-block", "app-client-secret", "lock-new", "lock-repeat",
    "lock-change-old", "lock-change-new", "lock-change-repeat", "lock-off-password", "remove-confirm",
    "lan-streaming-password", "lan-full-password"]) {
    $(id).value = "";
  }
  $("transfer-code").textContent = "";
  show($("transfer-area"), false);
  for (const id of ["add-result", "v-result", "vaults-notice", "account-result"]) show($(id), false);
  closeSheet();
  closeBrowser();
  closeRemoveDialog();
  if (cancelPasswordDialog) cancelPasswordDialog();
}

async function unlock() {
  const field = $("unlock-password");
  await run($("unlock-result"), async () => {
    setNotice($("unlock-result"), "info", snapshot && (snapshot.unlockConnects || []).length > 0 ?
      "Unlocking and connecting the drives…" : "Unlocking…");
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
  if (!confirm("Reset CryptBridge?\n\nThe pCloud sign-in, all vaults with their keys and the app password are deleted " +
      "from this PC. The files of your vaults stay untouched.\n\nTo open your vaults again you need password and salt " +
      "from their emergency sheets.")) return;
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
    "for the app password after starting. Keep your emergency sheets: they open the vaults even if you forget the app password.");
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

// ---------- LAN access ----------

// The two accesses, by the name the settings and the snapshot give them; the
// form fields of each are called lan-<role>-….
const LAN_ROLES = ["streaming", "full"];
const LAN_NAMES = { streaming: "Streaming access", full: "Full access" };

// anyLANAccessOn reports whether at least one access is on, in the snapshot
// or in the settings about to be saved.
function anyLANAccessOn(lan) {
  return LAN_ROLES.some((role) => lan[role].enabled);
}

// renderLAN shows what the servers do. The form fields are filled only by
// loadLANFields, so that typing is never overwritten.
function renderLAN(s) {
  for (const role of LAN_ROLES) {
    const access = s.lan[role];
    let state = "Off";
    if (access.enabled && access.running) {
      const drives = access.folders.length === 1 ? "1 drive" : `${access.folders.length} drives`;
      state = `On: ${drives} shared (${access.folders.join(", ")}).`;
    } else if (access.enabled && access.problem) {
      state = "On, but not working: " + access.problem;
    } else if (access.enabled) {
      state = "On, waiting for a connected drive.";
    }
    $(`lan-${role}-state`).textContent = state;
    show($(`lan-${role}-address-block`), access.enabled);
    renderLANAddresses($(`lan-${role}-addresses`), access.addresses);
  }
}

// renderLANAddresses lists the addresses with a Copy button each; the list
// is rebuilt only when it changes, so that a button keeps its "Copied".
function renderLANAddresses(list, addresses) {
  const shown = addresses.join(" ");
  if (list.dataset.shown === shown && list.childElementCount > 0) return;
  list.dataset.shown = shown;
  list.replaceChildren();
  if (addresses.length === 0) {
    list.append(el("div", "value", "No home network address found — is this PC connected to your router?"));
    return;
  }
  for (const address of addresses) {
    const copy = el("button", "link", "Copy");
    copy.addEventListener("click", () => copyWithFeedback(copy, address));
    const row = el("div", "row tight");
    row.append(el("code", "copy-text", address), copy);
    list.append(row);
  }
}

async function loadLANFields() {
  show($("lan-result"), false);
  try {
    const cfg = await api.LANSettings();
    for (const role of LAN_ROLES) {
      const access = cfg[role];
      $(`lan-${role}-enabled`).checked = access.enabled;
      $(`lan-${role}-user`).value = access.user;
      $(`lan-${role}-port`).value = access.port;
      $(`lan-${role}-password`).type = "password";
      // A new password makes an access ready for turning it on; each access gets its own.
      $(`lan-${role}-password`).value = access.password || await api.NewLANPassword();
    }
  } catch (err) {
    setNotice($("lan-result"), "error", errorText(err));
  }
}

// lanAccessFields reads the form of one access.
function lanAccessFields(role) {
  return {
    enabled: $(`lan-${role}-enabled`).checked,
    port: Number($(`lan-${role}-port`).value),
    user: $(`lan-${role}-user`).value.trim(),
    password: $(`lan-${role}-password`).value,
  };
}

// lanPortsText names the ports of the accesses that are on, e.g. "ports 8080 and 8081".
function lanPortsText(lan) {
  const ports = LAN_ROLES.filter((role) => lan[role].enabled).map((role) => lan[role].port);
  return (ports.length === 1 ? "port " : "ports ") + ports.join(" and ");
}

// The last look at the Windows firewall; looking takes a few seconds, so an
// older look that ends late must not overwrite a newer one.
let firewall = null;
let firewallLook = 0;

async function checkFirewall() {
  const look = ++firewallLook;
  $("lan-firewall").textContent = "Checking…";
  let result = null;
  let failure = "";
  try {
    result = await api.LANFirewall();
  } catch (err) {
    failure = errorText(err);
  }
  if (look !== firewallLook) return firewall;
  firewall = result;
  renderFirewall(failure);
  return firewall;
}

function renderFirewall(failure) {
  const lan = snapshot.lan;
  const enabled = anyLANAccessOn(lan);
  const fw = firewall;
  const notes = $("lan-firewall-notes");
  notes.replaceChildren();
  let allow = false;
  let remove = false;
  let network = false;
  let text;
  if (!fw) {
    text = "Could not be checked: " + failure;
  } else if (!enabled) {
    text = fw.ruleFound ? "CryptBridge's rule is still there although the LAN access is off." :
      "Nothing is allowed — the LAN access is off.";
    remove = fw.ruleFound;
  } else if (fw.privateOff) {
    text = "The firewall is off for home networks, so nothing needs to be allowed.";
  } else if (fw.ruleFits) {
    text = `Allowed: devices of your home network may connect on ${lanPortsText(lan)}.`;
  } else {
    const rulePorts = (fw.rulePort.includes(",") ? "ports " : "port ") + (fw.rulePort || "?");
    text = fw.ruleFound ? `CryptBridge's rule does not fit the current settings (it is for ${rulePorts}).` :
      "CryptBridge's rule is not there yet — without it, the devices of your home network cannot connect.";
    allow = true;
  }
  if (fw && enabled && fw.blocking.length > 0) {
    notes.append(el("div", "notice warn", `Windows also has rules that block rclone.exe: ${fw.blocking.join(", ")}. ` +
      "They win over any rule that allows. Delete them in Windows Security → Firewall & network protection → " +
      "Advanced settings → Inbound Rules."));
  }
  const others = fw && enabled ? uniqueRules(fw.others) : [];
  if (others.length > 0) {
    notes.append(el("div", "notice warn", `Windows has other rules that let rclone.exe in: ${others.map(ruleText).join(", ")}. ` +
      "Windows creates such rules when rclone is allowed once. With them, devices may reach the LAN access beyond " +
      "your home network, too. Unless you need them for something else, remove them (Windows asks for permission; " +
      "should rclone need such a rule again some day, Windows asks again)."));
  }
  const publicNetworks = fw && enabled ? fw.networks.filter((n) => n.category === "Public") : [];
  if (publicNetworks.length > 0) {
    network = true;
    const names = publicNetworks.map((n) => `"${n.name}" (${n.adapter})`).join(", ");
    notes.append(el("div", "notice warn", `Windows counts your network ${names} as public. CryptBridge's rule ` +
      "applies only to private (home) networks, so it does not let devices in there. If it is your home network, " +
      "set it to private: Windows Settings → Network & internet → your connection (Ethernet or Wi-Fi) → " +
      "Network profile type → Private."));
  }
  $("lan-firewall").textContent = text;
  show($("btn-lan-allow"), allow);
  show($("btn-lan-remove-rule"), remove);
  show($("btn-lan-remove-others"), others.length > 0);
  show($("btn-lan-network"), network);
}

// uniqueRules drops repeats: Windows often keeps the same rule twice, once
// for TCP and once for UDP.
function uniqueRules(rules) {
  const seen = new Set();
  return rules.filter((r) => {
    const key = `${r.name}|${r.profile}|${r.port}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

const PROFILE_NAMES = { Private: "home networks", Public: "public networks", Domain: "company networks", Any: "all networks" };

// ruleText describes a firewall rule, e.g. "rclone.exe" (public networks, any port).
function ruleText(rule) {
  const networks = rule.profile.split(",").map((p) => PROFILE_NAMES[p.trim()] || p.trim()).join(" and ");
  const port = rule.port === "Any" || rule.port === "" ? "any port" : "port " + rule.port;
  return `"${rule.name}" (${networks}, ${port})`;
}

// Saving also takes care of the firewall: turning the LAN access on lets
// the home network in, turning it off removes the rule again. Windows asks
// for permission each time.
$("btn-save-lan").addEventListener("click", () => run($("lan-result"), async () => {
  const wasEnabled = anyLANAccessOn(snapshot.lan);
  const change = { streaming: lanAccessFields("streaming"), full: lanAccessFields("full") };
  const enabled = anyLANAccessOn(change);
  await api.UpdateLAN(change);
  render(await api.GetSnapshot());
  const fw = await checkFirewall();
  if (enabled && fw && !fw.ruleFits && !fw.privateOff) {
    setNotice($("lan-result"), "info", "Saved. Windows now asks for permission to let the devices of your home network in…");
    await api.AllowLANFirewall();
    await checkFirewall();
  } else if (!enabled && wasEnabled && fw && fw.ruleFound) {
    setNotice($("lan-result"), "info", "Saved. Windows now asks for permission to remove CryptBridge's firewall rule…");
    await api.RemoveLANFirewall();
    await checkFirewall();
  }
  setNotice($("lan-result"), "good", enabled ? "Saved. The LAN access is on." : "Saved. The LAN access is off.");
}));

$("btn-lan-allow").addEventListener("click", () => run($("lan-result"), async () => {
  await api.AllowLANFirewall();
  await checkFirewall();
}));
$("btn-lan-remove-rule").addEventListener("click", () => run($("lan-result"), async () => {
  await api.RemoveLANFirewall();
  await checkFirewall();
}));
$("btn-lan-remove-others").addEventListener("click", () => run($("lan-result"), async () => {
  await api.RemoveOtherLANRules();
  await checkFirewall();
}));
$("btn-lan-network").addEventListener("click", () => run($("lan-result"), () => api.OpenNetworkSettings()));
$("btn-lan-recheck").addEventListener("click", () => checkFirewall());
document.querySelectorAll("[data-new-password]").forEach((button) => button.addEventListener("click", async () => {
  const field = $(button.dataset.newPassword);
  field.value = await api.NewLANPassword();
  field.type = "text"; // it has to be typed on the devices
}));
document.querySelectorAll("[data-copy-password]").forEach((button) => button.addEventListener("click", () =>
  copyWithFeedback(button, $(button.dataset.copyPassword).value)));

// ---------- start ----------

async function init() {
  // Wails injects its bridge shortly after the page loads.
  for (let i = 0; i < 100 && !(window.go && window.go.main && window.runtime); i++) {
    await new Promise((r) => setTimeout(r, 50));
  }
  api = window.go.main.App;
  await refreshSystemTheme();
  window.runtime.EventsOn("snapshot", render);
  window.runtime.EventsOn("signin-link", (link) => { signInLink = link; show($("signin-link-row"), true); });
  window.runtime.EventsOn("download-progress", (pct) => { $("download-fill").style.width = pct + "%"; });
  applyAddMode();
  const redirectURL = await api.PCloudRedirectURL();
  document.querySelectorAll(".redirect-url").forEach((node) => { node.textContent = redirectURL; });
  const s = await api.GetSnapshot();
  render(s);
  loadAppFields();
  if (s.phase === "setup") openTab("vaults", "add");
}

init();
