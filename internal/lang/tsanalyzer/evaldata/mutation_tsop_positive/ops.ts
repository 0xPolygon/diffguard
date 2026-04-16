// Exercises the TS-specific operators strict_equality (===) and
// nullish_to_logical_or (??).

export function isExact(a: unknown, b: unknown): boolean {
  return a === b;
}

export function pickDefault(a: number | null): number {
  return a ?? 42;
}
