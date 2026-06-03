// PHASE7.md §10 — localStorage-backed settings (preset, rebinds,
// accessibility toggles, master volume, nick, server-list). Defaults on
// absent/corrupt. Server URLs are validated/normalized to an origin with
// the WHATWG URL parser (§10.3, codex iter-0 finding #8).

import { PRESETS, validateBindings, type Bindings, type Preset } from "./input.js";

export interface Settings {
  preset: Preset;
  bindings: Bindings;
  colorBlind: boolean;
  highContrast: boolean;
  // docs/superpowers/specs/2026-06-02-enhanced-graphics-design.md §5f —
  // Direction-D CRT scanline+vignette retro pass toggle. On by default;
  // boot flips it off under prefers-reduced-motion when no setting is saved.
  retroFx: boolean;
  masterVolume: number; // 0..1
  nick: string;
  servers: string[]; // validated, normalized origins
}

export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

export const SETTINGS_KEY = "isnipes.settings";
export const MAX_SERVERS = 8;

export function defaultSettings(): Settings {
  return {
    preset: "classic",
    bindings: { ...PRESETS.classic },
    colorBlind: false,
    highContrast: false,
    retroFx: true,
    masterVolume: 0.7,
    nick: "",
    servers: [],
  };
}

// In-memory fallback so the module is usable (and testable) where
// localStorage is absent.
function memoryStorage(): StorageLike {
  const m = new Map<string, string>();
  return {
    getItem: (k) => (m.has(k) ? m.get(k)! : null),
    setItem: (k, v) => void m.set(k, v),
    removeItem: (k) => void m.delete(k),
  };
}

const sharedMemory = memoryStorage();

function resolveStorage(s?: StorageLike): StorageLike {
  if (s) return s;
  const ls = (globalThis as { localStorage?: StorageLike }).localStorage;
  return ls ?? sharedMemory;
}

function clamp01(v: number): number {
  if (typeof v !== "number" || !Number.isFinite(v)) return 0.7;
  return v < 0 ? 0 : v > 1 ? 1 : v;
}

// loadSettings reads + validates, falling back to defaults for any
// missing/corrupt field. Never throws.
export function loadSettings(storage?: StorageLike): Settings {
  const st = resolveStorage(storage);
  const def = defaultSettings();
  let raw: string | null;
  try {
    raw = st.getItem(SETTINGS_KEY);
  } catch {
    return def;
  }
  if (!raw) return def;
  let parsed: Partial<Settings>;
  try {
    parsed = JSON.parse(raw) as Partial<Settings>;
  } catch {
    return def;
  }
  if (typeof parsed !== "object" || parsed === null) return def;

  const preset: Preset = parsed.preset === "modern" ? "modern" : "classic";
  // Accept stored bindings only if structurally valid; else preset defaults.
  let bindings = { ...PRESETS[preset] };
  if (parsed.bindings && typeof parsed.bindings === "object") {
    const candidate = { ...PRESETS[preset], ...parsed.bindings } as Bindings;
    if (validateBindings(candidate) === null) bindings = candidate;
  }
  return {
    preset,
    bindings,
    colorBlind: parsed.colorBlind === true,
    highContrast: parsed.highContrast === true,
    // retroFx defaults true: absent/non-false stored value → on (§5f).
    retroFx: parsed.retroFx !== false,
    masterVolume: clamp01(parsed.masterVolume as number),
    nick: typeof parsed.nick === "string" ? parsed.nick.slice(0, 24) : "",
    // Re-validate persisted servers: stored data is untrusted (could be
    // hand-edited), so normalize each through the same URL guard before
    // it re-enters Settings.servers (codex iter-3).
    servers: Array.isArray(parsed.servers)
      ? dedupe(
          parsed.servers
            .filter((s): s is string => typeof s === "string")
            .map((s) => normalizeServerUrl(s, pageProtocol()))
            .filter((s): s is string => s !== null),
        ).slice(0, MAX_SERVERS)
      : [],
  };
}

// pageProtocol reads the current page protocol, defaulting to https: so
// stored ws:// entries are treated as mixed-content (rejected) unless the
// page is actually http:.
function pageProtocol(): string {
  return (globalThis as { location?: { protocol?: string } }).location?.protocol ?? "https:";
}

function dedupe(xs: string[]): string[] {
  return [...new Set(xs)];
}

export function saveSettings(s: Settings, storage?: StorageLike): void {
  const st = resolveStorage(storage);
  try {
    st.setItem(SETTINGS_KEY, JSON.stringify(s));
  } catch {
    /* storage full / unavailable — in-memory state stands */
  }
}

export function resetSettings(storage?: StorageLike): void {
  const st = resolveStorage(storage);
  try {
    st.removeItem(SETTINGS_KEY);
  } catch {
    /* ignore */
  }
}

// normalizeServerUrl parses `raw` with the WHATWG URL parser and returns
// a normalized origin (`scheme://host[:port]`), or null if invalid.
// Rules (§10.3): only ws:/wss:; reject ws: on an https: page (mixed
// content); reject userinfo, query, and fragment; drop any path.
export function normalizeServerUrl(raw: string, pageProtocol: string): string | null {
  const trimmed = raw.trim();
  if (trimmed === "") return null;
  // Infer scheme for a bare host (no "://").
  const withScheme = /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(trimmed)
    ? trimmed
    : (pageProtocol === "https:" ? "wss://" : "ws://") + trimmed;
  let u: URL;
  try {
    u = new URL(withScheme);
  } catch {
    return null;
  }
  if (u.protocol !== "ws:" && u.protocol !== "wss:") return null;
  if (pageProtocol === "https:" && u.protocol === "ws:") return null; // mixed content
  if (u.username !== "" || u.password !== "") return null; // userinfo
  if (u.hash !== "" || u.search !== "") return null; // fragment / query
  if (u.hostname === "") return null;
  return `${u.protocol}//${u.host}`;
}

// addServer validates + dedupes + caps a server list (most-recent first).
export function addServer(servers: readonly string[], raw: string, pageProtocol: string): string[] | null {
  const origin = normalizeServerUrl(raw, pageProtocol);
  if (origin === null) return null;
  const next = [origin, ...servers.filter((s) => s !== origin)];
  return next.slice(0, MAX_SERVERS);
}

// resolveMatchSocketUrl resolves a lobby-issued gameSocketPath against
// the selected lobby origin. Rejects an absolute / cross-origin path so a
// malicious matchStarted cannot redirect the joinToken (§10.3). Returns a
// ws(s):// URL or null.
export function resolveMatchSocketUrl(gameSocketPath: string, lobbyWsOrigin: string): string | null {
  // Must be a server-relative path, not an absolute URL.
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(gameSocketPath)) return null;
  if (!gameSocketPath.startsWith("/")) return null;
  let base: URL;
  try {
    base = new URL(lobbyWsOrigin);
  } catch {
    return null;
  }
  if (base.protocol !== "ws:" && base.protocol !== "wss:") return null;
  let u: URL;
  try {
    u = new URL(gameSocketPath, base);
  } catch {
    return null;
  }
  // Resolution must not have escaped the lobby origin.
  if (u.host !== base.host || u.protocol !== base.protocol) return null;
  return u.toString();
}
