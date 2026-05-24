// PHASE7.md §12.1 — deterministic scene state for the test-gated
// `?scene=` golden harness (§12.2). Each builder returns fully-decoded
// render/HUD/lobby state (not wire bytes) so the SAME render/hud path
// the live client uses produces the golden frame. Everything here is a
// pure function of the scene name (no clock, no RNG) for reproducible
// pixels.

import { SUBTILE_PER_TILE, TileCode, type MazeView } from "./sim.js";
import type { Entity } from "./proto.js";
import type { SelfPredicted } from "./render.js";
import { type HudModel, emptyHudModel, orderRows } from "./hud.js";
import type { RoomDescriptor, LevelPreset } from "./lobby.js";
import { EntityKind } from "./registry.js";

export type SceneName =
  | "empty-lobby" | "full-lobby" | "in-match-hud" | "scoreboard" | "end-screen";

export const SCENE_NAMES: SceneName[] = [
  "empty-lobby", "full-lobby", "in-match-hud", "scoreboard", "end-screen",
];

export function isSceneName(s: string): s is SceneName {
  return (SCENE_NAMES as string[]).includes(s);
}

export interface LobbyScene {
  kind: "lobby";
  rooms: RoomDescriptor[];
  presets: LevelPreset[];
}

export interface MatchScene {
  kind: "match";
  maze: MazeView;
  selfId: number;
  self: SelfPredicted;
  entities: Entity[];
  hud: HudModel;
}

export type Scene = LobbyScene | MatchScene;

// A small deterministic maze: solid border + open interior.
function borderMaze(w: number, h: number): MazeView {
  return {
    W: w, H: h,
    at(tx, ty) {
      if (tx <= 0 || ty <= 0 || tx >= w - 1 || ty >= h - 1) return TileCode.Wall;
      return TileCode.Floor;
    },
  };
}

const sub = (tiles: number) => tiles * SUBTILE_PER_TILE;

function demoPresets(): LevelPreset[] {
  const out: LevelPreset[] = [];
  for (let li = 0; li < 26; li++) {
    for (let n = 1; n <= 9; n++) {
      out.push({
        letter: String.fromCharCode(65 + li),
        number: n,
        difficulty: li < 6 ? "Easy" : li < 13 ? "Medium" : li < 19 ? "Hard" : "Brutal",
        playerLives: Math.max(1, 10 - n),
        generators: 1 + (n % 4),
        maxSnipes: n * 6,
        description: "Preset preview.",
      });
    }
  }
  return out;
}

function demoRooms(): RoomDescriptor[] {
  return [
    { id: "ABC123", name: "Alice's room", players: 2, max: 4, mode: "ffa", level: { letter: "A", number: 1 }, state: "OPEN" },
    { id: "Z9MN44", name: "Sniper alley", players: 4, max: 4, mode: "ffa", level: { letter: "M", number: 5 }, state: "IN_MATCH" },
    { id: "Q7RT80", name: "noobs only", players: 1, max: 4, mode: "ffa", level: { letter: "C", number: 2 }, state: "OPEN" },
  ];
}

function matchEntities(): { selfId: number; self: SelfPredicted; entities: Entity[] } {
  const selfId = 1;
  const self: SelfPredicted = { x: sub(8), y: sub(6), facing: 3 /*E*/, flags: 0 };
  const entities: Entity[] = [
    { id: 2, kind: EntityKind.Snipe, hp: 1, facing: 7, flags: 0, x: sub(14), y: sub(9), vx: 0, vy: 0 },
    { id: 3, kind: EntityKind.Generator, hp: 3, facing: 0, flags: 0, x: sub(20), y: sub(5), vx: 0, vy: 0 },
    { id: 4, kind: EntityKind.Projectile, hp: 1, facing: 3, flags: 0, x: sub(11), y: sub(6), vx: 32, vy: 0 },
    { id: 5, kind: EntityKind.Player, hp: 2, facing: 5, flags: 0, x: sub(10), y: sub(12), vx: 0, vy: 0 },
  ];
  return { selfId, self, entities };
}

function matchHud(): HudModel {
  const m = emptyHudModel();
  const rows = orderRows([
    { id: 1, nick: "you", lives: 3, score: 120 },
    { id: 5, nick: "rival", lives: 2, score: 90 },
  ]);
  m.rows = rows;
  m.nickById = new Map(rows.map((r) => [r.id, r.nick]));
  m.hp = 2; m.lives = 3; m.score = 120;
  m.chat = [
    { fromId: 5, fromNick: "rival", text: "nice shot" },
    { fromId: 1, fromNick: "you", text: "thanks" },
  ];
  return m;
}

export function buildScene(name: SceneName): Scene {
  switch (name) {
    case "empty-lobby":
      return { kind: "lobby", rooms: [], presets: demoPresets() };
    case "full-lobby":
      return { kind: "lobby", rooms: demoRooms(), presets: demoPresets() };
    case "in-match-hud": {
      const { selfId, self, entities } = matchEntities();
      return { kind: "match", maze: borderMaze(30, 20), selfId, self, entities, hud: matchHud() };
    }
    case "scoreboard": {
      const { selfId, self, entities } = matchEntities();
      const hud = matchHud();
      hud.showScoreboard = true;
      return { kind: "match", maze: borderMaze(30, 20), selfId, self, entities, hud };
    }
    case "end-screen": {
      const { selfId, self, entities } = matchEntities();
      const hud = matchHud();
      hud.endDialog = {
        reason: 1, // LAST_STANDING
        winnerId: 1,
        rows: hud.rows,
      };
      return { kind: "match", maze: borderMaze(30, 20), selfId, self, entities, hud };
    }
  }
}
