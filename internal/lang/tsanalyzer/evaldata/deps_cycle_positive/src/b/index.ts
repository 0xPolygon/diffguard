import { aFn } from "../a";

export function bFn(x: number): number {
  if (x > 100) return x;
  return aFn(x - 1);
}
