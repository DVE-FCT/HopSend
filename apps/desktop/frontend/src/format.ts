export function duration(ms: number, state: string): string {
  if (state === "skipped" || !Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1) return "< 1 ms";
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms)} ms`;
}
