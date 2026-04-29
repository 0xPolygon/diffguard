// TypeScript counterpart: complex nested branches and logical chains so the
// TS complexity section FAILs.

export function badTs(a: number, b: number, c: number, d: number, e: number): number {
  let total = 0;
  if (a > 0 && b > 0) {
    if (c > 0 || d > 0) {
      for (let i = 0; i < a; i++) {
        if (i % 2 === 0 && e > 0) {
          total += i;
        } else if (i % 3 === 0 || e < 0) {
          total -= i;
        }
      }
    }
  }
  switch (a) {
    case 1:
      total += 1;
      break;
    case 2:
      total += 2;
      break;
    case 3:
      total += 3;
      break;
    default:
      total += 0;
  }
  return total;
}
