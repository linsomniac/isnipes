// PHASE7.md §10.2 — render palettes. `default` and `colorBlind` differ
// in hue choices (no critical info conveyed by red/green alone — HP is
// always shown as a number in the HUD, §8.1). `highContrast` thickens
// entity outlines. The Renderer takes a Palette; toggling rebuilds the
// maze cache because tile colors changed.

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
};

// applyHighContrast thickens outlines for legibility (§7.5). Returns a
// new palette; does not mutate the input.
export function applyHighContrast(p: Palette, on: boolean): Palette {
  return on ? { ...p, outlineWidth: 3 } : { ...p, outlineWidth: 1 };
}

export function selectPalette(colorBlind: boolean, highContrast: boolean): Palette {
  const base = colorBlind ? COLORBLIND_PALETTE : DEFAULT_PALETTE;
  return applyHighContrast(base, highContrast);
}
