// Loose test: only covers the positive branch. Most Tier-1 mutants
// (negate_conditional on x<0, boundary flips, return_value swaps)
// survive.
import { classify } from "./arith.ts";

const got = classify(5);
if (got !== 1) {
  console.error(`classify(5) = ${got}, want 1`);
  process.exit(1);
}
console.log("PASS");
