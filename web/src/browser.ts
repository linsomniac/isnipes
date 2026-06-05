// PHASE7.md §6–§12 — browser entry point. Builds the lobby + match DOM
// and wires the headless App/LobbyClient to the Phase 7 presentation
// layer (render, hud, input, audio, settings). This is the only
// browser-only module; bundled to dist/app.js, never imported by vitest.

import { App } from "./main.js";
import type { LobbyWS, MatchStarted, RoomDescriptor, LevelPreset } from "./lobby.js";
import {
  computeSchemaChecksum, decodeSnapshot, decodeMapInit, MsgType,
  encodeFrame, type Snapshot, type Entity,
} from "./proto.js";
import { NetClient, type WebSocketLike } from "./netClient.js";
import {
  resolveMatchSocketUrl, normalizeServerUrl, loadSettings, saveSettings, addServer,
  SETTINGS_KEY, type Settings,
} from "./settings.js";
import { selectPalette } from "./palette.js";
import { mazeViewFromMapInit } from "./maze.js";
import { Renderer, type RenderState, type SelfPredicted, type ResolvedOverlay } from "./render.js";
import { EntityRegistry } from "./registry.js";
import { OverlayManager, muzzleFrame, poofFrame } from "./anim.js";
import { AudioEngine, WebAudioSink } from "./audio.js";
import { InputController, PRESETS } from "./input.js";
import {
  decodeScoreboard, decodeMatchOver, buildEndDialog, appendChat, plotMinimap,
  reasonText, winnerLabel, type HudModel, emptyHudModel, type ChatLine,
} from "./hud.js";
import { buildScene, isSceneName, type MatchScene, type LobbyScene } from "./scenes.js";
import { RespawnSequencer } from "./death.js";

const CLIENT_VERSION = "v0";
// Logical render resolution = the fixed slice of maze always shown: at
// render.ts TILE_PX=32 that's 1280/32 × 960/32 = 40 × 30 tiles — 2× the prior
// 20 × 15 view (a wider zoom-out). The canvas backing store stays this size;
// fitCanvas() only scales the CSS display size to fill the window (preserving
// the 4:3 aspect, letterboxed). All camera/render math keys off the backing
// store, so the visible tile count is independent of window size.
const CANVAS_W = 1280;
const CANVAS_H = 960;

function wsBase(): string {
  const scheme = location.protocol === "https:" ? "wss://" : "ws://";
  return scheme + location.host;
}

function el(tag: string, attrs: Record<string, string>, txt = ""): HTMLElement {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "hidden") e.hidden = true;
    else e.setAttribute(k, v);
  }
  if (txt) e.textContent = txt;
  return e;
}

// ---- DOM scaffold ----

interface UI {
  connecting: HTMLElement;
  lobby: HTMLElement;
  createBtn: HTMLButtonElement;
  nickInput: HTMLInputElement;
  myRoom: HTMLElement;
  myRoomId: HTMLElement;
  myRoomPlayers: HTMLElement;
  myRoomLink: HTMLAnchorElement;
  startBtn: HTMLButtonElement;
  startHint: HTMLElement;
  roomList: HTMLElement;
  picker: HTMLElement;
  pickerPreview: HTMLElement;
  serverInput: HTMLInputElement;
  serverList: HTMLElement;
  cbToggle: HTMLInputElement;
  hcToggle: HTMLInputElement;
  rfxToggle: HTMLInputElement;
  volSlider: HTMLInputElement;
  presetSelect: HTMLSelectElement;
  match: HTMLElement;
  matchView: HTMLElement;
  canvas: HTMLCanvasElement;
  minimap: HTMLCanvasElement;
  stats: HTMLElement;
  scoreboard: HTMLElement;
  chatBox: HTMLElement;
  chatInput: HTMLInputElement;
  endDialog: HTMLElement;
  backBtn: HTMLButtonElement;
  status: HTMLElement;
  respawnOverlay: HTMLElement;
}

function buildDOM(settings: Settings): UI {
  document.body.innerHTML = "";
  const status = el("p", { id: "status", "data-testid": "status" });
  const connecting = el("section", { "data-testid": "connecting" }, "Connecting…");

  // lobby
  const createBtn = el("button", { id: "create-room", "data-testid": "create-room" }, "Create Room") as HTMLButtonElement;
  const nickInput = el("input", { id: "nick", "data-testid": "nick-input", value: settings.nick }) as HTMLInputElement;
  nickInput.value = settings.nick;

  const myRoomId = el("span", { "data-testid": "my-room-id" });
  const myRoomPlayers = el("span", { "data-testid": "room-players" });
  const startBtn = el("button", { id: "start-btn", "data-testid": "start-btn" }, "Start match") as HTMLButtonElement;
  startBtn.disabled = true;
  const startHint = el("span", { "data-testid": "start-hint" });
  const myRoomLink = el("a", { "data-testid": "room-link", target: "_blank", rel: "noopener" }) as HTMLAnchorElement;
  const myRoom = el("div", { id: "my-room", "data-testid": "my-room", hidden: "true" });
  myRoom.append(text("Your room: "), myRoomId, text(" ("), myRoomPlayers, text(")  "), startBtn, text(" "), startHint);
  const invite = el("div", { "data-testid": "invite" });
  invite.append(text("Invite a player — open this link in another browser window: "), myRoomLink);
  myRoom.append(invite);

  const roomList = el("ul", { id: "room-list", "data-testid": "room-list" });
  const picker = el("div", { id: "level-picker", "data-testid": "level-picker" });
  const pickerPreview = el("div", { "data-testid": "level-preview" });

  // settings panel
  const presetSelect = el("select", { "data-testid": "preset-select" }) as HTMLSelectElement;
  for (const p of ["classic", "modern"]) presetSelect.append(el("option", { value: p }, p));
  presetSelect.value = settings.preset;
  const cbToggle = el("input", { type: "checkbox", "data-testid": "colorblind-toggle" }) as HTMLInputElement;
  cbToggle.checked = settings.colorBlind;
  const hcToggle = el("input", { type: "checkbox", "data-testid": "highcontrast-toggle" }) as HTMLInputElement;
  hcToggle.checked = settings.highContrast;
  const rfxToggle = el("input", { type: "checkbox", "data-testid": "retrofx-toggle" }) as HTMLInputElement;
  rfxToggle.checked = settings.retroFx;
  const volSlider = el("input", { type: "range", min: "0", max: "100", "data-testid": "volume" }) as HTMLInputElement;
  volSlider.value = String(Math.round(settings.masterVolume * 100));
  const serverInput = el("input", { "data-testid": "server-input", placeholder: "ws host" }) as HTMLInputElement;
  const serverAddBtn = el("button", { "data-testid": "server-add" }, "Add Server") as HTMLButtonElement;
  const serverList = el("ul", { "data-testid": "server-list" });
  const settingsPanel = el("section", { "data-testid": "settings" });
  settingsPanel.append(
    el("h3", {}, "Settings"),
    labeled("Nick", nickInput), labeled("Preset", presetSelect),
    labeled("Color-blind", cbToggle), labeled("High contrast", hcToggle),
    labeled("Retro FX", rfxToggle),
    labeled("Volume", volSlider), labeled("Server", serverInput), serverAddBtn, serverList,
  );

  const howto = el("ol", { "data-testid": "howto" });
  for (const step of [
    "Pick a level below (optional — A1 is the default).",
    "Click “Create Room” to open a room you host.",
    "Get a second player in: open your room’s invite link in another browser window, or have them click “Join” next to your room in the list. A match needs at least 2 players.",
    "Click “Start match” once someone has joined.",
  ]) howto.append(el("li", {}, step));
  const howtoBox = el("section", { "data-testid": "howto-box" });
  howtoBox.append(el("h2", {}, "How to start a game"), howto);

  const lobby = el("section", { id: "lobby", "data-testid": "lobby", hidden: "true" });
  lobby.append(
    el("h1", {}, "Lobby"), howtoBox, createBtn, myRoom,
    el("h2", {}, "Levels"), picker, pickerPreview,
    el("h2", {}, "Rooms"), roomList, settingsPanel,
  );

  // match
  const canvas = el("canvas", { id: "game", width: String(CANVAS_W), height: String(CANVAS_H) }) as HTMLCanvasElement;
  const minimap = el("canvas", { id: "minimap", "data-testid": "minimap", width: "120", height: "80" }) as HTMLCanvasElement;
  const stats = el("div", { "data-testid": "hud-stats" });
  const scoreboard = el("div", { "data-testid": "scoreboard", hidden: "true" });
  const chatBox = el("div", { "data-testid": "chat" });
  const chatInput = el("input", { "data-testid": "chat-input", hidden: "true" }) as HTMLInputElement;
  const backBtn = el("button", { "data-testid": "back-to-lobby" }, "Back to lobby") as HTMLButtonElement;
  const endDialog = el("div", { "data-testid": "end-dialog", hidden: "true" });
  const respawnOverlay = el("div", { "data-testid": "respawn-overlay", hidden: "true" });
  const matchView = el("div", { "data-testid": "match-view" });
  matchView.append(canvas, minimap, stats, scoreboard, chatBox, chatInput, endDialog, respawnOverlay);
  const match = el("section", { id: "match", hidden: "true" });
  match.append(matchView);

  document.body.append(status, connecting, lobby, match);

  const ui: UI = {
    connecting, lobby, createBtn, nickInput, myRoom, myRoomId, myRoomPlayers, myRoomLink,
    startBtn, startHint, roomList, picker, pickerPreview, serverInput, serverList, cbToggle,
    hcToggle, rfxToggle, volSlider, presetSelect, match, matchView, canvas, minimap, stats, scoreboard,
    chatBox, chatInput, endDialog, backBtn, status, respawnOverlay,
  };
  serverAddBtn.onclick = () => {
    const next = addServer(settings.servers, serverInput.value, location.protocol);
    if (next) { settings.servers = next; saveSettings(settings); renderServerList(ui, settings); serverInput.value = ""; }
  };
  return ui;
}

function labeled(name: string, control: HTMLElement): HTMLElement {
  const l = el("label", {});
  l.append(text(name + " "), control);
  return l;
}

function text(s: string): Text {
  return document.createTextNode(s);
}

// injectMatchStyles installs the in-match layout once: the match section fills
// the viewport (letterbox black), the game canvas is centred and scaled by
// fitCanvas, and the HUD/minimap/dialogs float as overlays so the playfield can
// use the whole window. Scoped to #match, so the lobby is untouched.
function injectMatchStyles(): void {
  if (document.getElementById("isnipes-match-style")) return;
  const style = document.createElement("style");
  style.id = "isnipes-match-style";
  style.textContent = `
#match { position: fixed; inset: 0; background: #000; overflow: hidden; }
/* AIDEV-NOTE: match-view wraps only absolutely-positioned children (canvas/HUD/
   dialogs), so without this it collapses to 0px height and Playwright's
   toBeVisible() (deeplink/play e2e) reports it hidden. inset:0 makes it fill
   #match without moving #game (which stays centred on the same 1280x720 box). */
#match [data-testid="match-view"] { position: absolute; inset: 0; }
#match #game { position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); background: #000; }
#match #minimap { position: absolute; top: 8px; right: 8px; width: 180px; height: 120px; border: 1px solid #2b3a55; background: #111; image-rendering: pixelated; }
#match [data-testid="hud-stats"] { position: absolute; top: 8px; left: 8px; margin: 0; color: #cfe3ff; font: 14px/1.4 monospace; text-shadow: 0 0 4px #000, 0 0 4px #000; pointer-events: none; }
#match [data-testid="chat"] { position: absolute; left: 8px; bottom: 36px; max-width: 44ch; color: #dfe7ff; font: 13px/1.35 monospace; text-shadow: 0 0 4px #000; }
#match [data-testid="chat-input"] { position: absolute; left: 8px; bottom: 8px; width: 44ch; }
#match [data-testid="scoreboard"], #match [data-testid="end-dialog"] { position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); background: rgba(8, 12, 20, 0.92); color: #eaf2ff; padding: 16px 24px; border: 1px solid #2b3a55; border-radius: 6px; font: 14px/1.5 monospace; min-width: 240px; }
#match [data-testid="respawn-overlay"] { position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); color: #ff5a5a; font: 700 28px/1.2 monospace; letter-spacing: 2px; text-shadow: 0 0 8px #000, 0 0 12px #000; pointer-events: none; }
`;
  document.head.append(style);
}

// injectLobbyStyles installs the Command Deck theme once, scoped to #lobby
// (the match view has its own injected styles). The lobby stays in normal
// document flow (min-height shell) so document.body keeps a non-zero height —
// a position:fixed lobby would collapse body to 0px and break Playwright
// visibility / scene-ready markers.
function injectLobbyStyles(): void {
  if (document.getElementById("isnipes-lobby-style")) return;
  const style = document.createElement("style");
  style.id = "isnipes-lobby-style";
  style.textContent = `
body { margin: 0; background: #07070b; }
#lobby.lobby-shell {
  --panel: rgba(18,22,32,.72); --line: #234a66; --line-dim: #1d3247;
  --cyan: #4fd1ff; --cyan-bright: #8af0ff; --neon: #1aa0e6;
  --text: #cfe3ff; --text-dim: #86b9d8; --muted: #6f7a92;
  --lime: #8aff80; --yellow: #ffd166;
  position: relative; min-height: 100vh; box-sizing: border-box;
  margin: 0; padding: 18px 16px 28px;
  background: radial-gradient(1200px 600px at 50% -10%, #10131c 0%, #0a0a0f 60%);
  color: var(--text);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
#lobby.lobby-shell.crt::after {
  content: ""; position: fixed; inset: 0; pointer-events: none; z-index: 50;
  background: repeating-linear-gradient(0deg, rgba(0,0,0,.16) 0, rgba(0,0,0,.16) 1px, transparent 1px, transparent 3px);
  opacity: .5;
}
#lobby .deck { max-width: 980px; margin: 0 auto; display: flex; flex-direction: column; gap: 12px; }
#lobby .deck-hdr { display: flex; align-items: flex-end; justify-content: space-between; gap: 12px; flex-wrap: wrap; border-bottom: 1px solid var(--line-dim); padding-bottom: 10px; }
#lobby .brand { margin: 0; font-weight: 800; letter-spacing: 5px; font-size: 30px; color: var(--cyan-bright); text-shadow: 0 0 10px var(--neon), 0 0 22px var(--neon); }
#lobby .tag { margin-top: 2px; font-size: 10px; letter-spacing: 3px; color: var(--muted); }
#lobby .ident { display: flex; align-items: center; gap: 8px; font-size: 12px; }
#lobby .conn { color: var(--muted); }
#lobby .conn.live { color: var(--lime); }
#lobby #nick { background: #0e1622; border: 1px solid var(--line); border-radius: 5px; color: var(--text); font: inherit; font-size: 12px; padding: 4px 8px; width: 14ch; }
#lobby .deck-cols { display: flex; gap: 12px; align-items: flex-start; }
#lobby .deck-right { display: flex; flex-direction: column; gap: 12px; flex: 1; min-width: 0; }
#lobby .play { flex: 1.15; min-width: 0; }
#lobby .panel { border: 1px solid var(--line); border-radius: 10px; background: var(--panel); padding: 12px 13px; }
#lobby .panel.glow { border-color: var(--neon); box-shadow: 0 0 14px rgba(26,160,230,.25) inset; }
#lobby .ph { margin: 0 0 8px; font-size: 10px; letter-spacing: 2px; text-transform: uppercase; color: #5fb0e6; display: flex; align-items: center; justify-content: space-between; }
#lobby button { font-family: inherit; cursor: pointer; }
#lobby #create-room, #lobby #start-btn { display: inline-block; margin-top: 2px; font-size: 13px; font-weight: 700; color: #04121c; background: linear-gradient(#7fe9ff, var(--neon)); border: none; border-radius: 6px; padding: 8px 14px; letter-spacing: .5px; box-shadow: 0 0 10px rgba(26,160,230,.5); }
#lobby #start-btn:disabled { filter: grayscale(.7) brightness(.7); cursor: not-allowed; box-shadow: none; }
#lobby [data-testid="join-room"], #lobby .ghost-btn { font-size: 11px; color: var(--cyan); background: transparent; border: 1px solid #2f6088; border-radius: 5px; padding: 2px 9px; }
#lobby [data-testid="join-room"]:disabled { color: var(--muted); border-color: #26384a; cursor: not-allowed; }
#lobby #my-room { margin-top: 10px; font-size: 13px; line-height: 1.7; }
#lobby #my-room-id { color: var(--cyan-bright); font-weight: 700; }
#lobby [data-testid="invite"] { font-size: 11px; color: var(--text-dim); margin-top: 4px; }
#lobby [data-testid="room-link"] { color: var(--cyan); word-break: break-all; }
#lobby [data-testid="start-hint"] { color: var(--muted); font-size: 11px; }
#lobby #level-picker { display: grid; grid-template-columns: repeat(9, 1fr); gap: 4px; margin-top: 6px; max-height: 140px; overflow-y: auto; padding-right: 4px; }
#lobby [data-testid="level-cell"] { font-size: 10px; text-align: center; padding: 5px 0; border: 1px solid #294f6c; border-radius: 4px; color: #9fd6f0; background: #0e1c28; }
#lobby [data-testid="level-cell"]:hover { border-color: var(--cyan); }
#lobby [data-testid="level-cell"].on { background: #15405a; border-color: var(--cyan); color: #d6f3ff; box-shadow: 0 0 7px var(--neon); }
#lobby [data-testid="level-preview"] { font-size: 11px; color: var(--text-dim); margin-top: 8px; border-top: 1px dashed #21384c; padding-top: 7px; }
#lobby #room-list { list-style: none; margin: 0; padding: 0; font-size: 12px; }
#lobby [data-testid="room-row"] { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 4px 0; border-bottom: 1px solid var(--line-dim); }
#lobby [data-testid="no-rooms"] { color: var(--muted); font-style: italic; padding: 4px 0; }
#lobby [data-testid="last-match"] .winner { color: var(--yellow); font-weight: 700; }
#lobby [data-testid="last-match"] .reason { color: var(--text-dim); font-size: 11px; margin: 2px 0 6px; }
#lobby [data-testid="last-match"] .score-row { display: flex; justify-content: space-between; font-size: 12px; color: #bcd2e8; padding: 1px 0; }
#lobby .x-btn { background: transparent; border: none; color: var(--muted); font-size: 13px; }
#lobby .x-btn:hover { color: var(--cyan); }
#lobby .cheat { display: flex; gap: 16px; flex-wrap: wrap; align-items: center; font-size: 12px; color: #bcd2e8; }
#lobby .key { color: #04121c; background: #9fd6f0; border-radius: 3px; padding: 0 5px; font-weight: 700; font-size: 11px; }
#lobby [data-testid="howto-toggle"] { font-size: 10px; color: var(--cyan); background: transparent; border: 1px solid #2f6088; border-radius: 5px; padding: 1px 8px; letter-spacing: 1px; }
#lobby [data-testid="howto-full"] { margin-top: 10px; font-size: 12px; line-height: 1.6; color: var(--text-dim); border-top: 1px solid var(--line-dim); padding-top: 9px; }
#lobby [data-testid="howto-full"] h4 { margin: 10px 0 4px; color: #7fc6f0; font-size: 11px; letter-spacing: 1.5px; text-transform: uppercase; }
#lobby [data-testid="howto"] { margin: 4px 0 0; padding-left: 18px; }
#lobby .settings-details { border: 1px solid var(--line-dim); border-radius: 10px; background: rgba(14,18,26,.6); padding: 4px 12px; }
#lobby .settings-details > summary { cursor: pointer; font-size: 11px; letter-spacing: 2px; text-transform: uppercase; color: #5fb0e6; padding: 7px 0; }
#lobby .settings-details[open] > summary { border-bottom: 1px solid var(--line-dim); margin-bottom: 8px; }
#lobby [data-testid="settings"] h3 { display: none; }
#lobby [data-testid="settings"] label { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--text-dim); margin: 0 12px 8px 0; }
#lobby [data-testid="server-input"] { background: #0e1622; border: 1px solid var(--line); border-radius: 5px; color: var(--text); font: inherit; font-size: 12px; padding: 3px 7px; }
#lobby [data-testid="server-list"] { list-style: none; margin: 6px 0 0; padding: 0; font-size: 11px; }
#lobby [data-testid="server-select"] { background: transparent; border: none; color: var(--text-dim); }
@media (max-width: 720px) { #lobby .deck-cols { flex-direction: column; } }
`;
  document.head.append(style);
}

// fitCanvas scales the fixed-resolution canvas (CANVAS_W × CANVAS_H backing
// store) to fill the window while preserving its aspect ratio, letterboxing the
// remainder. Only the CSS display size changes — the backing store, and thus
// the visible tile count, is window-independent (the user's "always show the
// same amount of the maze, just scale it to fit").
function fitCanvas(canvas: HTMLCanvasElement): void {
  const aspect = CANVAS_W / CANVAS_H;
  const ww = window.innerWidth;
  const wh = window.innerHeight;
  let w = ww;
  let h = Math.round(ww / aspect);
  if (h > wh) {
    h = wh;
    w = Math.round(wh * aspect);
  }
  canvas.style.width = `${w}px`;
  canvas.style.height = `${h}px`;
}

// toggleFullscreen flips the document in/out of fullscreen (bound to "f"
// in-match). The fullscreenchange listener re-runs fitCanvas afterwards.
function toggleFullscreen(): void {
  try {
    if (document.fullscreenElement) {
      const p = document.exitFullscreen?.();
      if (p) p.catch(() => {});
    } else {
      const p = document.documentElement.requestFullscreen?.();
      if (p) p.catch(() => {});
    }
  } catch {
    /* fullscreen unsupported */
  }
}

const SERVER_KEY = "isnipes.server";

// chosenLobbyOrigin returns the selected server origin (validated and
// present in the saved list) or the page origin (§10.3). This is the
// origin the lobby WS connects to AND the base gameSocketPath resolves
// against.
function chosenLobbyOrigin(settings: Settings): string {
  let sel = "";
  try { sel = localStorage.getItem(SERVER_KEY) ?? ""; } catch { sel = ""; }
  if (sel && settings.servers.includes(sel)) {
    const norm = normalizeServerUrl(sel, location.protocol);
    if (norm) return norm;
  }
  return wsBase();
}

function renderServerList(ui: UI, settings: Settings): void {
  ui.serverList.innerHTML = "";
  const current = chosenLobbyOrigin(settings);
  // The page origin is always an implicit option.
  for (const s of [wsBase(), ...settings.servers]) {
    const selected = s === current;
    const row = el("li", { "data-testid": "server-row", "data-origin": s }, "");
    const btn = el("button", { "data-testid": "server-select" }, (selected ? "● " : "○ ") + s) as HTMLButtonElement;
    btn.onclick = () => {
      try { localStorage.setItem(SERVER_KEY, s); } catch { /* ignore */ }
      // Reconnecting mid-session is out of scope; reload to bind the new
      // lobby origin cleanly.
      location.reload();
    };
    row.append(btn);
    ui.serverList.append(row);
  }
}

function renderRooms(
  listEl: HTMLElement,
  rooms: RoomDescriptor[],
  onJoin?: (roomId: string) => void,
  myRoomId?: string | null,
): void {
  listEl.innerHTML = "";
  if (rooms.length === 0) {
    listEl.append(el("li", { "data-testid": "no-rooms" }, "No rooms yet — click “Create Room” to make one."));
    return;
  }
  for (const r of rooms) {
    const li = el("li", { "data-testid": "room-row", "data-room-id": r.id });
    li.append(text(`${r.id} — ${r.name} (${r.players}/${r.max}) [${r.state}]  `));
    if (onJoin && r.id !== myRoomId) {
      const joinBtn = el("button", {
        "data-testid": "join-room", "data-room-id": r.id,
      }, "Join") as HTMLButtonElement;
      // Can't join a room that's full or already in a match. The server
      // serializes RoomState in uppercase ("OPEN"/"STARTING"/...), so the
      // compare must be uppercase — "open" left the button always disabled.
      joinBtn.disabled = r.players >= r.max || r.state !== "OPEN";
      joinBtn.onclick = () => onJoin(r.id);
      li.append(joinBtn);
    }
    listEl.append(li);
  }
}

function renderPicker(ui: UI, presets: LevelPreset[], onSelect: (p: LevelPreset) => void): void {
  ui.picker.innerHTML = "";
  for (const p of presets) {
    const cell = el("button", {
      "data-testid": "level-cell", "data-level": `${p.letter}${p.number}`,
    }, `${p.letter}${p.number}`);
    cell.onclick = () => { onSelect(p); renderPreview(ui, p); };
    ui.picker.append(cell);
  }
}

function renderPreview(ui: UI, p: LevelPreset): void {
  ui.pickerPreview.textContent =
    `${p.letter}${p.number} — ${p.difficulty}: ${p.playerLives} lives, ${p.generators} generators, ${p.maxSnipes} snipes`;
  ui.pickerPreview.setAttribute("data-level", `${p.letter}${p.number}`);
}

// ---- HUD rendering (DOM overlays; reused by live + scene paths) ----

function renderHud(ui: UI, hud: HudModel): void {
  ui.stats.textContent = `HP ${hud.hp}  Lives ${hud.lives}  Score ${hud.score}`;
  ui.scoreboard.hidden = !hud.showScoreboard;
  if (hud.showScoreboard) {
    ui.scoreboard.innerHTML = "<h3>Scoreboard</h3>";
    for (const r of hud.rows) {
      ui.scoreboard.append(el("div", { "data-testid": "score-row" }, `${r.nick}  ${r.score}  (${r.lives})`));
    }
  }
  ui.chatBox.innerHTML = "";
  for (const c of hud.chat) {
    ui.chatBox.append(el("div", { "data-testid": "chat-line" }, `${c.fromNick}: ${c.text}`));
  }
  if (hud.endDialog) {
    ui.endDialog.hidden = false;
    ui.endDialog.innerHTML = "";
    const d = hud.endDialog;
    ui.endDialog.append(
      el("h2", {}, reasonText(d.reason)),
      el("p", { "data-testid": "winner" }, winnerLabel(d.winnerId, hud.nickById)),
    );
    for (const r of d.rows) {
      ui.endDialog.append(el("div", { "data-testid": "end-row" }, `${r.nick}  ${r.score}`));
    }
    ui.endDialog.append(ui.backBtn);
  } else {
    ui.endDialog.hidden = true;
  }
  if (hud.respawnCountdown != null) {
    ui.respawnOverlay.hidden = false;
    ui.respawnOverlay.textContent = `RESPAWNING ${hud.respawnCountdown}`;
  } else {
    ui.respawnOverlay.hidden = true;
  }
}

function drawMinimap(ui: UI, maze: { W: number; H: number }, self: SelfPredicted, entities: Entity[]): void {
  const ctx = ui.minimap.getContext("2d");
  if (!ctx) return;
  ctx.clearRect(0, 0, ui.minimap.width, ui.minimap.height);
  ctx.fillStyle = "#111";
  ctx.fillRect(0, 0, ui.minimap.width, ui.minimap.height);
  const worldW = maze.W * 256, worldH = maze.H * 256;
  const rect = { x: 0, y: 0, w: ui.minimap.width, h: ui.minimap.height };
  const pts = plotMinimap(rect, worldW, worldH, self.x, self.y, entities);
  for (const p of pts) {
    ctx.fillStyle = p.self ? "#4fd1ff" : "#ff5a5a";
    ctx.fillRect(p.px - 1, p.py - 1, 3, 3);
  }
}

// ---- live match runner ----

class MatchRunner {
  private ui: UI;
  private renderer: Renderer;
  private reg = new EntityRegistry();
  private overlays = new OverlayManager();
  private audio: AudioEngine;
  private input: InputController;
  private hud: HudModel = emptyHudModel();
  private latest: Snapshot | null = null;
  private maze: { W: number; H: number } | null = null;
  private mazeViewCache: import("./sim.js").MazeView | null = null;
  private renderTick = 0;
  private ws: WebSocket | null = null;
  private nc: NetClient | null = null;
  private inputTimer = 0;
  private clientTick = 0;
  private raf = 0;
  private onEnd: () => void;
  private lobbyOrigin: string;
  private chatKeyHandler: ((e: KeyboardEvent) => void) | null = null;
  private respawn = new RespawnSequencer();
  private lastKnownSelfId = 0;

  constructor(ui: UI, settings: Settings, lobbyOrigin: string, onEnd: () => void) {
    this.ui = ui;
    this.onEnd = onEnd;
    this.lobbyOrigin = lobbyOrigin;
    const ctx = ui.canvas.getContext("2d") as unknown as import("./render.js").RenderCtx;
    this.renderer = new Renderer(
      ctx,
      selectPalette(settings.colorBlind, settings.highContrast),
      undefined,
      { retroFx: settings.retroFx },
    );
    this.audio = new AudioEngine(new WebAudioSink(), this.reg, () => settings.masterVolume);
    this.input = new InputController(settings.bindings ?? PRESETS[settings.preset]);
  }

  connect(ms: MatchStarted, schemaChecksum: number): void {
    // §10.3: resolve gameSocketPath against the SELECTED lobby origin
    // (not a hardcoded page origin) before sending the joinToken.
    const url = resolveMatchSocketUrl(ms.gameSocketPath, this.lobbyOrigin);
    if (url === null) { this.ui.matchView.setAttribute("data-match-error", "bad-socket-path"); return; }
    const ws = new WebSocket(url);
    ws.binaryType = "arraybuffer";
    this.ws = ws;
    const nc = new NetClient(ws as unknown as WebSocketLike);
    this.nc = nc;
    nc.setEvents({
      onOpen: () => nc.sendMatchJoin(schemaChecksum, ms.joinToken),
      onFrame: (type, payload) => this.onFrame(type, payload),
    });
    nc.start();
    this.attachInput();
    this.startLoops();
  }

  private onFrame(type: number, payload: Uint8Array): void {
    this.ui.matchView.setAttribute("data-match-connected", "true");
    switch (type) {
      case MsgType.MapInit: {
        const view = mazeViewFromMapInit(decodeMapInit(payload));
        this.maze = { W: view.W, H: view.H };
        this.mazeViewCache = view;
        this.renderer.setMap(view);
        break;
      }
      case MsgType.Snapshot: {
        const s = decodeSnapshot(payload);
        this.latest = s;
        this.reg.update(s);
        break;
      }
      case MsgType.Event: {
        if (payload.length >= 10) {
          const dv = new DataView(payload.buffer, payload.byteOffset, payload.byteLength);
          const kind = dv.getUint8(0), actor = dv.getUint32(1, true), target = dv.getUint32(5, true), reason = dv.getUint8(9);
          this.audio.onEvent(kind, actor, target, reason);
          this.overlays.onEvent(this.reg, kind, actor, target, this.renderTick);
        }
        break;
      }
      case MsgType.Chat: {
        // S→C relay Chat is self-attributing: [u32 sender][u8 len][text].
        if (payload.length >= 5) {
          const dv = new DataView(payload.buffer, payload.byteOffset, payload.byteLength);
          const id = dv.getUint32(0, true);
          const n = payload[4];
          if (payload.length === 5 + n) {
            const txt = new TextDecoder().decode(payload.subarray(5, 5 + n));
            const line: ChatLine = { fromId: id, fromNick: this.hud.nickById.get(id) ?? `Player ${id}`, text: txt };
            this.hud.chat = appendChat(this.hud.chat, line);
          }
        }
        break;
      }
      case MsgType.Scoreboard: {
        const rows = decodeScoreboard(payload);
        this.hud.rows = rows;
        this.hud.nickById = new Map(rows.map((r) => [r.id, r.nick]));
        break;
      }
      case MsgType.MatchOver: {
        const mo = decodeMatchOver(payload);
        this.audio.onMatchOver(mo.reason);
        this.hud.endDialog = buildEndDialog(mo, this.hud.nickById);
        // Clear any in-flight respawn overlay so "RESPAWNING n" doesn't float
        // over the end-of-match dialog when a death also ends the match (e.g.
        // last-standing). The sequencer's own safety timeout would clear it
        // eventually, but the end screen is shown immediately.
        this.hud.respawnCountdown = null;
        this.onEnd();
        break;
      }
    }
  }

  private attachInput(): void {
    window.addEventListener("keydown", this.keydown);
    window.addEventListener("keyup", this.keyup);
  }

  private keydown = (e: KeyboardEvent): void => {
    // Chat input has focus → let the field handle typing.
    if (document.activeElement === this.ui.chatInput) return;
    // "f" toggles fullscreen (handled here, before input, so it isn't also
    // captured as a held key).
    if (e.code === "KeyF") { e.preventDefault(); toggleFullscreen(); return; }
    if (e.code === "Tab") { e.preventDefault(); this.hud.showScoreboard = true; return; }
    if (e.code === this.input.getBindings().chatOpen) {
      e.preventDefault();
      this.ui.chatInput.hidden = false;
      this.ui.chatInput.focus();
      return;
    }
    this.input.keyDown(e.code);
  };

  private keyup = (e: KeyboardEvent): void => {
    if (e.code === "Tab") { this.hud.showScoreboard = false; return; }
    this.input.keyUp(e.code);
  };

  private startLoops(): void {
    const loop = () => {
      this.renderTick++;
      this.drawFrame();
      this.raf = requestAnimationFrame(loop);
    };
    this.raf = requestAnimationFrame(loop);
    // Input send + chat input keys at ~30 Hz.
    this.inputTimer = window.setInterval(() => this.tickInput(), 33);
    // Stored so stop() can detach it — otherwise a stale runner's handler
    // would consume Enter against a closed socket in the next match.
    this.chatKeyHandler = (e: KeyboardEvent) => {
      if (e.code === "Enter") { this.sendChat(this.ui.chatInput.value); this.ui.chatInput.value = ""; this.ui.chatInput.hidden = true; this.ui.chatInput.blur(); }
      else if (e.code === "Escape") { this.ui.chatInput.value = ""; this.ui.chatInput.hidden = true; this.ui.chatInput.blur(); }
    };
    this.ui.chatInput.addEventListener("keydown", this.chatKeyHandler);
  }

  private tickInput(): void {
    // Only send once the socket is open (send() throws in CONNECTING).
    if (!this.nc || !this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    const intent = this.input.intent();
    this.nc.sendInput({ clientTick: this.clientTick++, dir: intent.dir, turbo: intent.turbo ? 1 : 0, fireDir: intent.fireDir });
  }

  private sendChat(textVal: string): void {
    const trimmed = textVal.trim();
    if (trimmed === "" || !this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    // Encode first, then trim to ≤255 bytes on a UTF-8 boundary so a
    // multi-byte char can't wrap the u8 length and get rejected.
    const enc = truncateUtf8(new TextEncoder().encode(trimmed), 255);
    const payload = new Uint8Array(1 + enc.length);
    payload[0] = enc.length;
    payload.set(enc, 1);
    this.ws.send(encodeFrame({ type: MsgType.Chat, flags: 0, seq: 0, ack: 0xffff, len: payload.length }, payload));
  }

  // resolveOverlays maps the active combat overlays (muzzle/poof) to
  // ResolvedOverlay{kind,x,y,frame} the renderer can blit (spec §5g): the
  // anchor's world position comes from the registry (latest snapshot), the
  // animation frame from muzzleFrame/poofFrame(renderTick - armedAtTick).
  // Overlays whose anchor is no longer in the snapshot (outside AOI / evicted)
  // are dropped — there's no on-screen position to draw them at.
  private resolveOverlays(): ResolvedOverlay[] {
    const out: ResolvedOverlay[] = [];
    for (const o of this.overlays.active(this.renderTick)) {
      const anchor = this.reg.get(o.anchorId);
      if (!anchor) continue;
      const age = this.renderTick - o.armedAtTick;
      const frame = o.kind === "muzzle" ? muzzleFrame(age) : poofFrame(age);
      out.push({ kind: o.kind, x: anchor.x, y: anchor.y, frame });
    }
    return out;
  }

  // setRetroFx flips the CRT pass on the live renderer (settings panel toggle).
  setRetroFx(on: boolean): void {
    this.renderer.setRetroFx(on);
  }

  private drawFrame(): void {
    const latest = this.latest;
    if (!latest || !this.maze) { renderHud(this.ui, this.hud); return; }
    const selfEntity = latest.entities.find((e) => e.id === latest.yourEntityID) ?? null;
    const self: SelfPredicted | null = selfEntity
      ? { x: selfEntity.x, y: selfEntity.y, facing: selfEntity.facing, flags: selfEntity.flags }
      : null;
    const others = latest.entities.filter((e) => e.id !== latest.yourEntityID);

    // Remember the live self id (stable across respawn) so we can read our
    // lives from the scoreboard while dead (yourEntityID is 0 then).
    if (latest.yourEntityID !== 0) this.lastKnownSelfId = latest.yourEntityID;
    const selfRow = this.hud.rows.find((r) => r.id === this.lastKnownSelfId);
    const livesRemaining = selfRow ? selfRow.lives : 1;
    const fx = this.respawn.update({
      selfPresent: self !== null,
      selfPos: self ? { x: self.x, y: self.y } : null,
      livesRemaining,
      nowMs: performance.now(),
    });

    this.renderer.draw(
      {
        map: this.mazeViewCache, selfId: latest.yourEntityID, selfPredicted: self,
        entities: others, renderTick: this.renderTick,
        overlays: this.resolveOverlays(),
        cameraOverride: fx.cameraOverride,
        deathFx: { redAlpha: fx.redAlpha, dimAlpha: fx.dimAlpha },
      },
      this.hud,
    );
    if (self) drawMinimap(this.ui, this.maze, self, others);
    // Test-gated self-position hook so the live e2e (#27) can assert
    // server-authoritative movement. Prod never sets __ISNIPES_TEST__.
    if (self && testMode()) {
      (window as unknown as { __isnipesSelf?: { x: number; y: number } }).__isnipesSelf = { x: self.x, y: self.y };
    }
    // self HUD stats from the self entity + the scoreboard row.
    if (selfEntity) this.hud.hp = selfEntity.hp;
    const row = this.hud.rows.find((r) => r.id === latest.yourEntityID);
    if (row) { this.hud.lives = row.lives; this.hud.score = row.score; }
    this.hud.respawnCountdown = fx.countdown;
    renderHud(this.ui, this.hud);
  }

  stop(): void {
    cancelAnimationFrame(this.raf);
    clearInterval(this.inputTimer);
    window.removeEventListener("keydown", this.keydown);
    window.removeEventListener("keyup", this.keyup);
    if (this.chatKeyHandler) { this.ui.chatInput.removeEventListener("keydown", this.chatKeyHandler); this.chatKeyHandler = null; }
    try { this.nc?.close(); } catch { /* ignore */ }
  }
}

// truncateUtf8 returns the longest prefix of `bytes` ≤ max that does not
// split a multi-byte UTF-8 sequence (continuation bytes are 0x80–0xBF).
function truncateUtf8(bytes: Uint8Array, max: number): Uint8Array {
  if (bytes.length <= max) return bytes;
  let end = max;
  while (end > 0 && (bytes[end] & 0xc0) === 0x80) end--;
  return bytes.subarray(0, end);
}

// ---- scene harness (test-gated; §12.2) ----

function testMode(): boolean {
  return (window as unknown as { __ISNIPES_TEST__?: boolean }).__ISNIPES_TEST__ === true;
}

function renderScene(ui: UI, name: import("./scenes.js").SceneName, settings: Settings): void {
  const scene = buildScene(name);
  // AIDEV-NOTE: scene-ready must mark a *visible* element. In a match scene the
  // lobby is hidden and #match is position:fixed, so document.body collapses to
  // 0px height — a marker on it reads as hidden and the golden match scenes time
  // out. Mark the shown root instead: body for lobby (normal flow), #match for a
  // match (fills the viewport). Both are test-gated, so prod (scene_guard) stays
  // marker-free.
  let readyRoot: HTMLElement;
  if (scene.kind === "lobby") {
    renderLobbyScene(ui, scene);
    readyRoot = document.body;
  } else {
    renderMatchScene(ui, scene, settings);
    readyRoot = ui.match;
  }
  readyRoot.setAttribute("data-testid", "scene-ready");
  ui.matchView.setAttribute("data-scene-ready", "true");
}

function renderLobbyScene(ui: UI, scene: LobbyScene): void {
  ui.connecting.hidden = true;
  ui.lobby.hidden = false;
  ui.match.hidden = true;
  renderRooms(ui.roomList, scene.rooms);
  renderPicker(ui, scene.presets, () => {});
  if (scene.presets.length > 0) renderPreview(ui, scene.presets[0]);
}

function renderMatchScene(ui: UI, scene: MatchScene, settings: Settings): void {
  ui.connecting.hidden = true;
  ui.lobby.hidden = true;
  ui.match.hidden = false;
  const ctx = ui.canvas.getContext("2d") as unknown as import("./render.js").RenderCtx;
  // Golden scenes render with Retro FX on (spec §5g): the CRT pass is part of
  // the baseline. settings.retroFx defaults true (loadSettings), but pass it
  // explicitly so the scene matches the live MatchRunner's renderer.
  const renderer = new Renderer(
    ctx,
    selectPalette(settings.colorBlind, settings.highContrast),
    undefined,
    { retroFx: settings.retroFx },
  );
  renderer.setMap(scene.maze);
  renderer.draw(
    { map: scene.maze, selfId: scene.selfId, selfPredicted: scene.self, entities: scene.entities, renderTick: 0 },
    scene.hud,
  );
  drawMinimap(ui, scene.maze, scene.self, scene.entities);
  renderHud(ui, scene.hud);
}

// ---- boot ----

// hasSavedSettings reports whether a settings blob has ever been persisted.
// Used at boot to decide the prefers-reduced-motion default: only a *fresh*
// user (no saved blob) gets Retro FX defaulted off when they've asked the OS
// to reduce motion — once they've saved a choice, it's respected (§5g).
function hasSavedSettings(): boolean {
  try {
    return (globalThis as { localStorage?: { getItem(k: string): string | null } })
      .localStorage?.getItem(SETTINGS_KEY) != null;
  } catch {
    return false;
  }
}

function prefersReducedMotion(): boolean {
  try {
    const mm = (globalThis as { matchMedia?: (q: string) => { matches: boolean } }).matchMedia;
    return mm ? mm("(prefers-reduced-motion: reduce)").matches : false;
  } catch {
    return false;
  }
}

async function boot(): Promise<void> {
  const settings = loadSettings();
  // First-run default: with no saved settings, honor the OS "reduce motion"
  // preference by turning the CRT/retro pass off (§5g). A returning user's
  // saved retroFx (true or false) is left untouched.
  if (!hasSavedSettings() && prefersReducedMotion()) settings.retroFx = false;
  injectMatchStyles();
  injectLobbyStyles();
  const ui = buildDOM(settings);
  renderServerList(ui, settings);
  // The live match renderer lives on the active MatchRunner (created later);
  // the Retro FX toggle reaches it through this getter so the CRT pass flips
  // mid-match without a reload (spec §5g).
  let runner: MatchRunner | null = null;
  applySettingsHandlers(ui, settings, () => runner);
  // Scale the playfield to fill the window (fixed aspect, letterboxed) now and
  // whenever the window or fullscreen state changes.
  fitCanvas(ui.canvas);
  window.addEventListener("resize", () => fitCanvas(ui.canvas));
  document.addEventListener("fullscreenchange", () => fitCanvas(ui.canvas));

  const schemaChecksum = await computeSchemaChecksum();

  // Test-gated golden-scene harness: prod never sets __ISNIPES_TEST__,
  // so a production bundle ignores ?scene= entirely (DoD #25).
  if (testMode()) {
    const scene = new URLSearchParams(location.search).get("scene");
    if (scene && isSceneName(scene)) { renderScene(ui, scene, settings); return; }
  }

  const lobbyOrigin = chosenLobbyOrigin(settings);
  const app = new App({
    openLobbyWS: () => new BrowserLobbyWS(lobbyOrigin + "/ws/lobby"),
    locationSearch: () => location.search,
    getStoredNick: () => settings.nick || defaultNick(settings),
    schemaChecksum,
    clientVersion: CLIENT_VERSION,
  });

  let myRoomId: string | null = null;
  let joinedRoomId: string | null = null;
  let pendingCreateName: string | null = null;
  let preCreateIds = new Set<string>();
  let selectedLevel = { letter: "A", number: 1 };

  const inviteLinkFor = (roomId: string): string =>
    `${location.origin}${location.pathname}?room=${roomId}`;

  const onJoinRoom = (roomId: string): void => {
    joinedRoomId = roomId;
    app.lobby.joinRoom(roomId);
    ui.status.textContent = `Joined room ${roomId} — waiting for the host to start…`;
  };

  ui.createBtn.onclick = () => {
    const name = "Room-" + Math.random().toString(36).slice(2, 6);
    pendingCreateName = name;
    preCreateIds = new Set(app.lobby.rooms().map((r) => r.id));
    app.lobby.createRoom({ name, max: 4, level: selectedLevel });
  };
  ui.startBtn.onclick = () => { if (myRoomId !== null) app.lobby.startMatch(myRoomId); };
  ui.backBtn.onclick = () => {
    runner?.stop(); runner = null;
    app.backToLobby();
    ui.match.hidden = true; ui.lobby.hidden = false; ui.endDialog.hidden = true;
  };

  app.onWelcome = () => { ui.connecting.hidden = true; ui.lobby.hidden = false; };
  app.lobby.onLevelPresets = (presets) => {
    renderPicker(ui, presets, (p) => { selectedLevel = { letter: p.letter, number: p.number }; });
    if (presets.length > 0) renderPreview(ui, presets[0]);
  };
  app.lobby.onRoomListChange = (rooms) => {
    if (myRoomId === null && pendingCreateName !== null) {
      const mine = rooms.find((r) => r.name === pendingCreateName && !preCreateIds.has(r.id));
      if (mine) {
        myRoomId = mine.id; pendingCreateName = null;
        ui.myRoomId.textContent = mine.id; ui.myRoom.hidden = false;
        ui.myRoomLink.href = inviteLinkFor(mine.id);
        ui.myRoomLink.textContent = inviteLinkFor(mine.id);
      }
    }
    renderRooms(ui.roomList, rooms, onJoinRoom, myRoomId ?? joinedRoomId);
    if (myRoomId !== null) {
      const mine = rooms.find((r) => r.id === myRoomId);
      if (mine) {
        ui.myRoomPlayers.textContent = `${mine.players}/${mine.max}`;
        const canStart = mine.players >= 2;
        ui.startBtn.disabled = !canStart;
        ui.startHint.textContent = canStart ? "" : "(waiting for a second player to join…)";
      }
    }
  };
  app.onMatchStarted = (ms: MatchStarted) => {
    ui.lobby.hidden = true; ui.match.hidden = false; ui.endDialog.hidden = true;
    runner = new MatchRunner(ui, settings, lobbyOrigin, () => app.endMatch());
    runner.connect(ms, schemaChecksum);
  };
  app.lobby.onError = (e) => { ui.status.textContent = `error: ${e.code} ${e.message}`; };

  app.start();
}

function applySettingsHandlers(ui: UI, settings: Settings, getRunner: () => MatchRunner | null): void {
  ui.nickInput.onchange = () => { settings.nick = ui.nickInput.value.slice(0, 24); saveSettings(settings); };
  ui.presetSelect.onchange = () => {
    settings.preset = ui.presetSelect.value === "modern" ? "modern" : "classic";
    settings.bindings = PRESETS[settings.preset];
    saveSettings(settings);
  };
  ui.cbToggle.onchange = () => { settings.colorBlind = ui.cbToggle.checked; saveSettings(settings); };
  ui.hcToggle.onchange = () => { settings.highContrast = ui.hcToggle.checked; saveSettings(settings); };
  // Retro FX toggle: persist + flip the CRT pass on the live renderer (§5g).
  ui.rfxToggle.onchange = () => {
    settings.retroFx = ui.rfxToggle.checked;
    saveSettings(settings);
    getRunner()?.setRetroFx(settings.retroFx);
  };
  ui.volSlider.oninput = () => { settings.masterVolume = Number(ui.volSlider.value) / 100; saveSettings(settings); };
}

function defaultNick(settings: Settings): string {
  if (settings.nick) return settings.nick;
  settings.nick = "Player-" + Math.random().toString(36).slice(2, 7);
  saveSettings(settings);
  return settings.nick;
}

// BrowserLobbyWS adapts the browser WebSocket to the LobbyWS surface.
class BrowserLobbyWS implements LobbyWS {
  private ws: WebSocket;
  onmessage: ((data: string) => void) | null = null;
  onclose: (() => void) | null = null;
  onopen: (() => void) | null = null;
  constructor(url: string) {
    this.ws = new WebSocket(url);
    this.ws.onopen = () => this.onopen?.();
    this.ws.onclose = () => this.onclose?.();
    this.ws.onmessage = (ev: MessageEvent) => { if (typeof ev.data === "string") this.onmessage?.(ev.data); };
  }
  send(data: string): void { this.ws.send(data); }
  close(): void { this.ws.close(); }
}

void boot();
