// Seeded: nested ternaries + try/catch + long logical chains drive
// cognitive complexity above the default 10 threshold.

export function tangled(a: number | null, b: number | null, flag: boolean): number {
  let total = 0;
  try {
    if (a !== null && b !== null) {
      if (a > 0 && (b > 0 || flag)) {
        for (let i = 0; i < a; i++) {
          if (i % 2 === 0 && flag) {
            total += i > 10 ? i * 2 : i;
          } else if (i % 3 === 0 || b < 0) {
            total -= b > 5 ? b : 1;
          }
        }
      } else {
        switch (a) {
          case 1:
            total = 1;
            break;
          case 2:
            total = 2;
            break;
          case 3:
            total = 3;
            break;
          default:
            total = -1;
        }
      }
    }
  } catch (e) {
    total = -1;
  }
  return total;
}
