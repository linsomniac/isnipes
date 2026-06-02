// PHASE7.md §10.2 — render palettes. `default` and `colorBlind` differ
// in hue choices (no critical info conveyed by red/green alone — HP is
// always shown as a number in the HUD, §8.1). `highContrast` thickens
// entity outlines. The Renderer takes a Palette; toggling rebuilds the
// maze cache because tile colors changed.
//
// docs/superpowers/specs/2026-06-02-enhanced-graphics-design.md §5a —
// extended (additively) for Direction D enhanced graphics: neon maze
// stroke/glow + floor dither, the glowing self ground-ring, per-player
// suit hues, and the CRT/bloom retro-FX knobs. All original fields are
// preserved so colorForKind (and its tests) keep working unchanged.
// The new fields are pure data consumed by the baked atlas / maze / CRT
// surfaces (spriteAtlas.ts, render.ts neon rasterize, crt.ts).

// PLAYER_HUE_COUNT is mirrored in spriteAtlas.ts; playerHues MUST have
// exactly this many entries ([0] = self/cyan). Kept here (not imported
// from spriteAtlas) so palette.ts has no dependency on the canvas layer.
export const PLAYER_HUE_COUNT = 7;

export interface Palette {
  name: string;
  bg: string;
  wall: string;
  floor: string;
  floorAlt: string;
  self: string;
  player: string;
  snipe: string;
  generator: string;
  generatorDamaged: string;
  projectile: string;
  hudText: string;
  hudBg: string;
  outlineWidth: number;
  // --- Direction D enhanced-graphics fields (spec §5a) ---
  wallStroke: string; // neon maze line color
  wallGlow: string; // maze stroke glow (shadowColor) color
  floorDither: string; // subtle floor speckle color
  selfRing: string; // glowing ground-ring under local player
  playerHues: string[]; // per-player suit hues; [0] = self (cyan); length == PLAYER_HUE_COUNT
  bloom: boolean; // bake glow into atlas/maze (false under high-contrast)
  scanlineAlpha: number; // 0..1 CRT scanline darkness (0 → none)
  vignetteAlpha: number; // 0..1 CRT vignette strength
}

export const DEFAULT_PALETTE: Palette = {
  name: "default",
  bg: "#0a0a0f",
  wall: "#2b2b40",
  floor: "#14141c",
  floorAlt: "#191922",
  self: "#4fd1ff",
  player: "#8aff80",
  snipe: "#ff5a5a",
  generator: "#ffd166",
  generatorDamaged: "#b07a1f",
  projectile: "#ffffff",
  hudText: "#e6e6f0",
  hudBg: "rgba(0,0,0,0.55)",
  outlineWidth: 1,
  // Neon maze: cyan glowing walls over a dark dithered floor (spec §6).
  wallStroke: "#1aa0e6",
  wallGlow: "#34bdf0",
  floorDither: "rgba(80,120,180,0.08)",
  selfRing: "#6ee6ff",
  // 7 distinct suit hues; [0] = self cyan, then green/orange/purple/
  // teal/pink/yellow (ported from the validated Direction-D preview).
  playerHues: [
    "#4fd1ff", // 0 self — cyan
    "#2f9f4a", // 1 green
    "#d98a2a", // 2 orange
    "#8a52d6", // 3 purple
    "#2bb5a3", // 4 teal
    "#e85aa8", // 5 pink
    "#e3c63a", // 6 yellow
  ],
  bloom: true,
  scanlineAlpha: 0.18,
  vignetteAlpha: 0.55,
};

// Color-blind safe (blue/orange/yellow primary axis instead of
// green/red). Hostiles are orange, friendlies blue — distinguishable
// under deuteranopia/protanopia. HP-as-number is the non-color backstop.
export const COLORBLIND_PALETTE: Palette = {
  ...DEFAULT_PALETTE,
  name: "colorBlind",
  self: "#56b4e9",
  player: "#0072b2",
  snipe: "#e69f00",
  generator: "#f0e442",
  generatorDamaged: "#9c8a12",
  projectile: "#ffffff",
  // Hostiles read orange, friendlies on a blue axis; the maze stays a
  // distinct blue so it never collides with the orange snipes. Self ring
  // is sky-blue to match the self suit.
  wallStroke: "#56b4e9",
  wallGlow: "#7fc7ef",
  selfRing: "#9fd6f5",
  // Okabe–Ito-derived, colorblind-safe suit hues. [0] = self (sky blue).
  playerHues: [
    "#56b4e9", // 0 self — sky blue
    "#009e73", // 1 bluish green
    "#e69f00", // 2 orange
    "#0072b2", // 3 blue
    "#cc79a7", // 4 reddish purple
    "#f0e442", // 5 yellow
    "#d55e00", // 6 vermillion
  ],
};

// applyHighContrast thickens outlines for legibility (§7.5). Returns a
// new palette; does not mutate the input.
//
// Direction D (spec §5a): high-contrast also drops the retro bloom and
// CRT scanlines (which reduce legibility) and brightens the neon maze
// strokes to flat near-white. The vignette is kept (it does not obscure
// entities). When off, all enhanced fields pass through unchanged so the
// caller-selected base palette's bloom/scanline settings survive.
export function applyHighContrast(p: Palette, on: boolean): Palette {
  if (!on) return { ...p, outlineWidth: 1 };
  return {
    ...p,
    outlineWidth: 3,
    bloom: false,
    scanlineAlpha: 0,
    // Brighter, flatter strokes read clearly without the glow pass.
    wallStroke: "#cfe8ff",
    wallGlow: "#cfe8ff",
    selfRing: "#e6f6ff",
  };
}

export function selectPalette(colorBlind: boolean, highContrast: boolean): Palette {
  const base = colorBlind ? COLORBLIND_PALETTE : DEFAULT_PALETTE;
  return applyHighContrast(base, highContrast);
}
