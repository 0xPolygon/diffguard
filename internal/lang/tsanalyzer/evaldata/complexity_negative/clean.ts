// Negative control: same overall work split into helpers. Each stays
// well under the cognitive threshold.

export function sign(n: number): number {
  if (n > 0) return 1;
  if (n < 0) return -1;
  return 0;
}

export function doubled(n: number | null): number {
  return (n ?? 0) * 2;
}

export function classify(a: number): string {
  return a === 1 ? "one" : "other";
}
