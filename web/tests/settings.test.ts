// PHASE7.md §15.8 — DoD #23/#24. localStorage round-trip + defaults +
// reset, and server-URL validation/normalization.

import { describe, expect, test } from "vitest";
import {
  loadSettings, saveSettings, resetSettings, defaultSettings,
  normalizeServerUrl, addServer, resolveMatchSocketUrl,
  SETTINGS_KEY, MAX_SERVERS, type StorageLike, type Settings,
} from "../src/settings.js";
import { PRESETS } from "../src/input.js";

function fakeStorage(seed?: Record<string, string>): StorageLike & { map: Map<string, string> } {
  const map = new Map<string, string>(Object.entries(seed ?? {}));
  return {
    map,
    getItem: (k) => (map.has(k) ? map.get(k)! : null),
    setItem: (k, v) => void map.set(k, v),
    removeItem: (k) => void map.delete(k),
  };
}

describe("settings persistence", () => {
  test("TestSettings_RoundTripLocalStorage", () => {
    const st = fakeStorage();
    const s: Settings = {
      preset: "modern",
      bindings: { ...PRESETS.modern, turbo: "KeyQ" },
      colorBlind: true,
      highContrast: true,
      retroFx: false,
      masterVolume: 0.4,
      nick: "Ada",
      servers: ["wss://play.example.com"],
    };
    saveSettings(s, st);
    const back = loadSettings(st);
    expect(back.preset).toBe("modern");
    expect(back.bindings.turbo).toBe("KeyQ");
    expect(back.colorBlind).toBe(true);
    expect(back.highContrast).toBe(true);
    expect(back.retroFx).toBe(false);
    expect(back.masterVolume).toBe(0.4);
    expect(back.nick).toBe("Ada");
    expect(back.servers).toEqual(["wss://play.example.com"]);
  });

  // §5f — retroFx defaults true when absent, and only an explicit `false`
  // turns it off (any other value / missing key → on).
  test("retroFx defaults true and round-trips", () => {
    // default is true.
    expect(defaultSettings().retroFx).toBe(true);
    expect(loadSettings(fakeStorage()).retroFx).toBe(true);
    // stored object without retroFx → defaults true.
    const noKey = fakeStorage({ [SETTINGS_KEY]: JSON.stringify({ preset: "classic" }) });
    expect(loadSettings(noKey).retroFx).toBe(true);
    // explicit false persists.
    const off = fakeStorage({ [SETTINGS_KEY]: JSON.stringify({ retroFx: false }) });
    expect(loadSettings(off).retroFx).toBe(false);
  });

  test("TestSettings_DefaultsOnCorrupt", () => {
    expect(loadSettings(fakeStorage())).toEqual(defaultSettings()); // absent
    const bad = fakeStorage({ [SETTINGS_KEY]: "{not json" });
    expect(loadSettings(bad)).toEqual(defaultSettings());
    const wrongType = fakeStorage({ [SETTINGS_KEY]: "42" });
    expect(loadSettings(wrongType)).toEqual(defaultSettings());
  });

  test("invalid stored bindings fall back to preset defaults", () => {
    const st = fakeStorage({
      [SETTINGS_KEY]: JSON.stringify({ preset: "classic", bindings: { fireN: "ArrowUp" } }),
    });
    const s = loadSettings(st);
    // ArrowUp collides with moveN → invalid → classic defaults used.
    expect(s.bindings.fireN).toBe(PRESETS.classic.fireN);
  });

  test("masterVolume is clamped on load", () => {
    const st = fakeStorage({ [SETTINGS_KEY]: JSON.stringify({ masterVolume: 9 }) });
    expect(loadSettings(st).masterVolume).toBe(1);
  });

  test("TestSettings_ResetClearsKey", () => {
    const st = fakeStorage();
    saveSettings(defaultSettings(), st);
    expect(st.map.has(SETTINGS_KEY)).toBe(true);
    resetSettings(st);
    expect(st.map.has(SETTINGS_KEY)).toBe(false);
    expect(loadSettings(st)).toEqual(defaultSettings());
  });
});

describe("server-list URL validation", () => {
  test("TestSettings_ServerListUrlValidation", () => {
    // bare host → inferred scheme + origin only.
    expect(normalizeServerUrl("play.example.com", "http:")).toBe("ws://play.example.com");
    expect(normalizeServerUrl("play.example.com", "https:")).toBe("wss://play.example.com");
    // explicit wss with a path → origin only (path dropped).
    expect(normalizeServerUrl("wss://h.example:9000/match", "https:")).toBe("wss://h.example:9000");
    // mixed content: ws:// on an https: page → rejected.
    expect(normalizeServerUrl("ws://h.example", "https:")).toBeNull();
    // userinfo / query / fragment → rejected.
    expect(normalizeServerUrl("wss://user:pass@h.example", "https:")).toBeNull();
    expect(normalizeServerUrl("wss://h.example?x=1", "https:")).toBeNull();
    expect(normalizeServerUrl("wss://h.example#frag", "https:")).toBeNull();
    // non-ws scheme → rejected.
    expect(normalizeServerUrl("http://h.example", "https:")).toBeNull();
    expect(normalizeServerUrl("javascript:alert(1)", "https:")).toBeNull();
    // garbage → null, not throw.
    expect(normalizeServerUrl("   ", "https:")).toBeNull();
  });

  test("addServer dedupes (most-recent first) and caps at MAX_SERVERS", () => {
    let servers: string[] = [];
    servers = addServer(servers, "a.example", "http:")!;
    servers = addServer(servers, "b.example", "http:")!;
    servers = addServer(servers, "a.example", "http:")!; // re-add → moves to front
    expect(servers).toEqual(["ws://a.example", "ws://b.example"]);
    expect(addServer(servers, "ws://user:pass@bad", "http:")).toBeNull();
    // cap.
    let many: string[] = [];
    for (let i = 0; i < MAX_SERVERS + 3; i++) many = addServer(many, `h${i}.example`, "http:")!;
    expect(many.length).toBe(MAX_SERVERS);
  });

  test("resolveMatchSocketUrl: relative path against lobby origin; reject cross-origin", () => {
    expect(resolveMatchSocketUrl("/ws/match/AB12", "wss://h.example:8080"))
      .toBe("wss://h.example:8080/ws/match/AB12");
    // absolute / cross-origin path → rejected (token-redirect guard).
    expect(resolveMatchSocketUrl("wss://evil.example/ws/match/AB12", "wss://h.example:8080")).toBeNull();
    expect(resolveMatchSocketUrl("//evil.example/x", "wss://h.example:8080")).toBeNull();
    expect(resolveMatchSocketUrl("ws/match/AB12", "wss://h.example:8080")).toBeNull(); // not rooted
  });
});
