// PHASE4.md §11 — non-self entity interpolation buffer. Renders
// entities at a target time `nowWallMs - targetLagMs` (default 100
// ms) by lerping between the two snapshots straddling that time.
// Self-entity is handled by prediction.ts (no interpolation lag);
// this buffer covers every other entity in the snapshot.

export interface EntityState {
  id: number;
  kind: number;
  hp: number;
  facing: number;
  flags: number;
  x: number;
  y: number;
  vx: number;
  vy: number;
}

interface SnapshotFrame {
  serverTick: number;
  wallMs: number;
  entities: EntityState[];
}

const DEFAULT_LAG_MS = 100;

// Snapshots older than (nowWallMs - STALE_HORIZON_MS) are dropped.
const STALE_HORIZON_MS = 1000;

export class InterpBuffer {
  private readonly targetLagMs: number;
  private buf: SnapshotFrame[] = [];

  constructor(targetLagMs: number = DEFAULT_LAG_MS) {
    this.targetLagMs = targetLagMs;
  }

  size(): number {
    return this.buf.length;
  }

  // push records a snapshot received at wall-clock `recvWallMs` for
  // server tick `serverTick`. The buffer is kept in ascending receive
  // wall-time (wallMs) order — sample()/pruneStale key off wallMs, not
  // serverTick (which is retained only as informational metadata). Stale
  // entries are pruned.
  push(serverTick: number, recvWallMs: number, entities: EntityState[]): void {
    this.buf.push({ serverTick, wallMs: recvWallMs, entities });
    // Keep wall-time ascending.
    this.buf.sort((a, b) => a.wallMs - b.wallMs);
    this.pruneStale(recvWallMs);
  }

  // sample returns the interpolated entity list at the target render
  // time = nowWallMs - targetLagMs. Cold-start (empty/single) falls
  // back to the latest snapshot directly.
  sample(nowWallMs: number): EntityState[] {
    if (this.buf.length === 0) return [];
    const tRender = nowWallMs - this.targetLagMs;
    // Find sBefore = latest snapshot with wallMs <= tRender.
    let sBefore: SnapshotFrame | undefined;
    let sAfter: SnapshotFrame | undefined;
    for (let i = 0; i < this.buf.length; i++) {
      const s = this.buf[i];
      if (s.wallMs <= tRender) sBefore = s;
      else if (sAfter === undefined) sAfter = s;
    }
    if (!sBefore) {
      // Render time is older than every snapshot — use the oldest.
      return this.buf[0].entities.map(cloneEntity);
    }
    if (!sAfter) {
      // Render time is newer than every snapshot — use the freshest.
      return sBefore.entities.map(cloneEntity);
    }
    const span = sAfter.wallMs - sBefore.wallMs;
    if (span <= 0) return sBefore.entities.map(cloneEntity);
    const alpha = (tRender - sBefore.wallMs) / span;
    return lerpEntities(sBefore.entities, sAfter.entities, alpha);
  }

  clear(): void {
    this.buf = [];
  }

  private pruneStale(nowWallMs: number): void {
    const horizon = nowWallMs - STALE_HORIZON_MS;
    while (this.buf.length > 1 && this.buf[0].wallMs < horizon) {
      this.buf.shift();
    }
  }
}

function cloneEntity(e: EntityState): EntityState {
  return { ...e };
}

function lerpEntities(
  before: EntityState[],
  after: EntityState[],
  alpha: number,
): EntityState[] {
  const byID = new Map<number, EntityState>();
  for (const e of after) byID.set(e.id, e);
  const out: EntityState[] = [];
  for (const b of before) {
    const a = byID.get(b.id);
    if (!a) {
      // Entity disappeared in `after`; carry forward unchanged.
      out.push(cloneEntity(b));
      continue;
    }
    out.push({
      id: b.id,
      kind: a.kind,
      hp: a.hp,
      facing: a.facing,
      flags: a.flags,
      x: Math.round(b.x + (a.x - b.x) * alpha),
      y: Math.round(b.y + (a.y - b.y) * alpha),
      vx: a.vx,
      vy: a.vy,
    });
  }
  // Entities present in `after` but not `before` appear once alpha
  // pushes past 0 — emit them as-is on `after`.
  for (const a of after) {
    if (!before.some((b) => b.id === a.id)) {
      out.push(cloneEntity(a));
    }
  }
  return out;
}
