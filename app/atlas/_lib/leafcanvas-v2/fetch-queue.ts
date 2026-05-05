/**
 * fetch-queue.ts — module-level priority queue for canonical_tree + asset
 * fetches. Prevents the "80-frame stampede" where every frame mounts at
 * once and races Figma's 5-req/sec render budget, leaving most as grey
 * placeholders.
 *
 * Pattern adapted from DesignBrain-AI's UploadBudget.ts (P0/P1/P2/P3
 * priority bands with a per-frame budget cap). For our DOM canvas we
 * gate by request-count instead of bytes — Figma rate limits at the
 * upstream level, browsers cap concurrent connections per origin at 6,
 * and our ds-service has its own per-tenant token bucket. So 8/sec is
 * a safe ceiling that won't spike either side.
 *
 * Priority bands:
 *
 *   P0  — visible in viewport, user is interacting (drag / select)
 *   P1  — visible in viewport, idle
 *   P2  — adjacent to viewport (HOT/WARM IO band)
 *   P3  — off-screen, pre-fetch only
 *
 * A request enqueued at higher priority overtakes lower-priority queued
 * requests. Already-running requests are not preempted; queueing is
 * cooperative, not interrupting.
 */

export type FetchPriority = 0 | 1 | 2 | 3;

interface Pending {
  priority: FetchPriority;
  enqueueIdx: number; // FIFO tiebreaker within same priority
  run: () => Promise<void>;
}

class FetchQueue {
  /** Max requests in flight at once. Above this, enqueue + wait. */
  readonly concurrency: number;
  /** Soft floor on inter-request spacing (ms). Skipped when concurrency slots are free; respected once they fill. */
  readonly minSpacingMs: number;

  private inFlight = 0;
  private pending: Pending[] = [];
  private enqueueCounter = 0;
  private lastDrainAt = 0;

  constructor(concurrency = 8, minSpacingMs = 50) {
    this.concurrency = concurrency;
    this.minSpacingMs = minSpacingMs;
  }

  /** Enqueue a request. Returns when the work resolves. */
  schedule<T>(priority: FetchPriority, work: () => Promise<T>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const run = async (): Promise<void> => {
        this.inFlight++;
        this.lastDrainAt = Date.now();
        try {
          const result = await work();
          resolve(result);
        } catch (err) {
          reject(err instanceof Error ? err : new Error(String(err)));
        } finally {
          this.inFlight--;
          this.drain();
        }
      };
      this.pending.push({ priority, enqueueIdx: this.enqueueCounter++, run });
      // Sort by (priority asc, enqueueIdx asc) — lower priority value =
      // higher precedence. Stable within priority via enqueueIdx.
      this.pending.sort((a, b) =>
        a.priority !== b.priority ? a.priority - b.priority : a.enqueueIdx - b.enqueueIdx,
      );
      this.drain();
    });
  }

  /**
   * Lightweight stats — used by tests and the canvas header to surface
   * "loading 12 / 30" style indicators.
   */
  stats(): { inFlight: number; pending: number } {
    return { inFlight: this.inFlight, pending: this.pending.length };
  }

  /**
   * Re-prioritize a request that was enqueued at lower priority — used
   * when a frame transitions from WARM (P3 prefetch) to HOT (P1 visible)
   * mid-fetch and we want it to jump the queue. The caller passes the
   * same `work` reference to find the entry. No-op if not found (request
   * is already in flight or completed).
   */
  promote(work: () => Promise<unknown>, newPriority: FetchPriority): void {
    const idx = this.pending.findIndex((p) => p.run === work);
    if (idx < 0) return;
    const entry = this.pending[idx];
    if (entry && entry.priority > newPriority) {
      entry.priority = newPriority;
      this.pending.sort((a, b) =>
        a.priority !== b.priority ? a.priority - b.priority : a.enqueueIdx - b.enqueueIdx,
      );
    }
  }

  private drain(): void {
    while (this.inFlight < this.concurrency && this.pending.length > 0) {
      const since = Date.now() - this.lastDrainAt;
      if (this.inFlight >= this.concurrency - 1 && since < this.minSpacingMs) {
        // Almost-full + recently drained → throttle the last slot to
        // smooth out the burst. setTimeout re-enters drain.
        setTimeout(() => this.drain(), this.minSpacingMs - since);
        return;
      }
      const next = this.pending.shift();
      if (!next) break;
      void next.run();
    }
  }
}

/** Singleton — one queue for the whole atlas surface. */
export const canvasFetchQueue = new FetchQueue(8, 50);
