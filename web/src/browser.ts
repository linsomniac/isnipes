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
import { resolveMatchSocketUrl } from "./settings.js";
import {
  loadSettings, saveSettings, addServer, type Settings,
} from "./settings.js";
import { selectPalette } from "./palette.js";
import { mazeViewFromMapInit } from "./maze.js";
import { Renderer, type RenderState, type SelfPredicted } from "./render.js";
import { EntityRegistry } from "./registry.js";
import { OverlayManager } from "./anim.js";
import { AudioEngine, WebAudioSink } from "./audio.js";
import { InputController, PRESETS } from "./input.js";
import {
  decodeScoreboard, decodeMatchOver, buildEndDialog, appendChat, plotMinimap,
  reasonText, winnerLabel, type HudModel, emptyHudModel, type ChatLine,
} from "./hud.js";
import { buildScene, isSceneName, type MatchScene, type LobbyScene } from "./scenes.js";

const CLIENT_VERSION = "v0";
const CANVAS_W = 640;
const CANVAS_H = 480;

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
  startBtn: HTMLButtonElement;
  roomList: HTMLElement;
  picker: HTMLElement;
  pickerPreview: HTMLElement;
  serverInput: HTMLInputElement;
  serverList: HTMLElement;
  cbToggle: HTMLInputElement;
  hcToggle: HTMLInputElement;
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
  const startBtn = el("button", { id: "start-btn", "data-testid": "start-btn" }, "Start") as HTMLButtonElement;
  startBtn.disabled = true;
  const myRoom = el("div", { id: "my-room", "data-testid": "my-room", hidden: "true" });
  myRoom.append(text("Your room: "), myRoomId, text(" ("), myRoomPlayers, text(")  "), startBtn);

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
    labeled("Volume", volSlider), labeled("Server", serverInput), serverAddBtn, serverList,
  );

  const lobby = el("section", { id: "lobby", "data-testid": "lobby", hidden: "true" });
  lobby.append(
    el("h1", {}, "Lobby"), createBtn, myRoom,
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
  const matchView = el("div", { "data-testid": "match-view" });
  matchView.append(canvas, minimap, stats, scoreboard, chatBox, chatInput, endDialog);
  const match = el("section", { id: "match", hidden: "true" });
  match.append(matchView);

  document.body.append(status, connecting, lobby, match);

  const ui: UI = {
    connecting, lobby, createBtn, nickInput, myRoom, myRoomId, myRoomPlayers, startBtn,
    roomList, picker, pickerPreview, serverInput, serverList, cbToggle, hcToggle, volSlider,
    presetSelect, match, matchView, canvas, minimap, stats, scoreboard, chatBox, chatInput,
    endDialog, backBtn, status,
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

function renderServerList(ui: UI, settings: Settings): void {
  ui.serverList.innerHTML = "";
  for (const s of settings.servers) ui.serverList.append(el("li", { "data-testid": "server-row" }, s));
}

function renderRooms(listEl: HTMLElement, rooms: RoomDescriptor[]): void {
  listEl.innerHTML = "";
  for (const r of rooms) {
    const li = el("li", { "data-testid": "room-row", "data-room-id": r.id });
    li.textContent = `${r.id} — ${r.name} (${r.players}/${r.max}) [${r.state}]`;
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

  constructor(ui: UI, settings: Settings, onEnd: () => void) {
    this.ui = ui;
    this.onEnd = onEnd;
    const ctx = ui.canvas.getContext("2d") as unknown as import("./render.js").RenderCtx;
    this.renderer = new Renderer(ctx, selectPalette(settings.colorBlind, settings.highContrast));
    this.audio = new AudioEngine(new WebAudioSink(), this.reg, () => settings.masterVolume);
    this.input = new InputController(settings.bindings ?? PRESETS[settings.preset]);
  }

  connect(ms: MatchStarted, schemaChecksum: number): void {
    const url = resolveMatchSocketUrl(ms.gameSocketPath, wsBase());
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
          if (payload.length >= 5 + n) {
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
    this.ui.chatInput.addEventListener("keydown", (e) => {
      if (e.code === "Enter") { this.sendChat(this.ui.chatInput.value); this.ui.chatInput.value = ""; this.ui.chatInput.hidden = true; this.ui.chatInput.blur(); }
      else if (e.code === "Escape") { this.ui.chatInput.value = ""; this.ui.chatInput.hidden = true; this.ui.chatInput.blur(); }
    });
  }

  private tickInput(): void {
    if (!this.nc) return;
    const intent = this.input.intent();
    this.nc.sendInput({ clientTick: this.clientTick++, dir: intent.dir, turbo: intent.turbo ? 1 : 0, fireDir: intent.fireDir });
  }

  private sendChat(textVal: string): void {
    const trimmed = textVal.trim();
    if (trimmed === "" || !this.ws) return;
    const enc = new TextEncoder().encode(trimmed.slice(0, 255));
    const payload = new Uint8Array(1 + enc.length);
    payload[0] = enc.length;
    payload.set(enc, 1);
    this.ws.send(encodeFrame({ type: MsgType.Chat, flags: 0, seq: 0, ack: 0xffff, len: payload.length }, payload));
  }

  private drawFrame(): void {
    const latest = this.latest;
    if (!latest || !this.maze) { renderHud(this.ui, this.hud); return; }
    const selfEntity = latest.entities.find((e) => e.id === latest.yourEntityID) ?? null;
    const self: SelfPredicted | null = selfEntity
      ? { x: selfEntity.x, y: selfEntity.y, facing: selfEntity.facing, flags: selfEntity.flags }
      : null;
    const others = latest.entities.filter((e) => e.id !== latest.yourEntityID);
    this.renderer.draw(
      { map: this.mazeViewCache, selfId: latest.yourEntityID, selfPredicted: self, entities: others, renderTick: this.renderTick },
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
    renderHud(this.ui, this.hud);
  }

  stop(): void {
    cancelAnimationFrame(this.raf);
    clearInterval(this.inputTimer);
    window.removeEventListener("keydown", this.keydown);
    window.removeEventListener("keyup", this.keyup);
    try { this.nc?.close(); } catch { /* ignore */ }
  }
}

// ---- scene harness (test-gated; §12.2) ----

function testMode(): boolean {
  return (window as unknown as { __ISNIPES_TEST__?: boolean }).__ISNIPES_TEST__ === true;
}

function renderScene(ui: UI, name: import("./scenes.js").SceneName, settings: Settings): void {
  const scene = buildScene(name);
  if (scene.kind === "lobby") {
    renderLobbyScene(ui, scene);
  } else {
    renderMatchScene(ui, scene, settings);
  }
  document.body.setAttribute("data-testid", "scene-ready");
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
  const renderer = new Renderer(ctx, selectPalette(settings.colorBlind, settings.highContrast));
  renderer.setMap(scene.maze);
  renderer.draw(
    { map: scene.maze, selfId: scene.selfId, selfPredicted: scene.self, entities: scene.entities, renderTick: 0 },
    scene.hud,
  );
  drawMinimap(ui, scene.maze, scene.self, scene.entities);
  renderHud(ui, scene.hud);
}

// ---- boot ----

async function boot(): Promise<void> {
  const settings = loadSettings();
  const ui = buildDOM(settings);
  renderServerList(ui, settings);
  applySettingsHandlers(ui, settings);

  const schemaChecksum = await computeSchemaChecksum();

  // Test-gated golden-scene harness: prod never sets __ISNIPES_TEST__,
  // so a production bundle ignores ?scene= entirely (DoD #25).
  if (testMode()) {
    const scene = new URLSearchParams(location.search).get("scene");
    if (scene && isSceneName(scene)) { renderScene(ui, scene, settings); return; }
  }

  const app = new App({
    openLobbyWS: () => new BrowserLobbyWS(wsBase() + "/ws/lobby"),
    locationSearch: () => location.search,
    getStoredNick: () => settings.nick || defaultNick(settings),
    schemaChecksum,
    clientVersion: CLIENT_VERSION,
  });

  let myRoomId: string | null = null;
  let pendingCreateName: string | null = null;
  let preCreateIds = new Set<string>();
  let selectedLevel = { letter: "A", number: 1 };
  let runner: MatchRunner | null = null;

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
      if (mine) { myRoomId = mine.id; pendingCreateName = null; ui.myRoomId.textContent = mine.id; ui.myRoom.hidden = false; }
    }
    renderRooms(ui.roomList, rooms);
    if (myRoomId !== null) {
      const mine = rooms.find((r) => r.id === myRoomId);
      if (mine) { ui.myRoomPlayers.textContent = `${mine.players}/${mine.max}`; ui.startBtn.disabled = mine.players < 2; }
    }
  };
  app.onMatchStarted = (ms: MatchStarted) => {
    ui.lobby.hidden = true; ui.match.hidden = false; ui.endDialog.hidden = true;
    runner = new MatchRunner(ui, settings, () => app.endMatch());
    runner.connect(ms, schemaChecksum);
  };
  app.lobby.onError = (e) => { ui.status.textContent = `error: ${e.code} ${e.message}`; };

  app.start();
}

function applySettingsHandlers(ui: UI, settings: Settings): void {
  ui.nickInput.onchange = () => { settings.nick = ui.nickInput.value.slice(0, 24); saveSettings(settings); };
  ui.presetSelect.onchange = () => {
    settings.preset = ui.presetSelect.value === "modern" ? "modern" : "classic";
    settings.bindings = PRESETS[settings.preset];
    saveSettings(settings);
  };
  ui.cbToggle.onchange = () => { settings.colorBlind = ui.cbToggle.checked; saveSettings(settings); };
  ui.hcToggle.onchange = () => { settings.highContrast = ui.hcToggle.checked; saveSettings(settings); };
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
