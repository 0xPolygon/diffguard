export function isExact(a: unknown, b: unknown): boolean {
  return a === b;
}

export function pickDefault(a: number | null): number {
  return a ?? 42;
}
