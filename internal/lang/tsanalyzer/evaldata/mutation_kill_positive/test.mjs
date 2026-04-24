// Boundary + sign + zero coverage: kills Tier-1 mutants
// (conditional_boundary, negate_conditional, math_operator, return_value).
import { classify } from "./arith.ts";

const cases = [
  [5, 1],
  [-5, -1],
  [0, 0],
  [1, 1],
  [-1, -1],
];

let failed = 0;
for (const [input, expected] of cases) {
  const got = classify(input);
  if (got !== expected) {
    console.error(`classify(${input}) = ${got}, want ${expected}`);
    failed++;
  }
}

if (failed > 0) {
  process.exit(1);
}
console.log("PASS");
