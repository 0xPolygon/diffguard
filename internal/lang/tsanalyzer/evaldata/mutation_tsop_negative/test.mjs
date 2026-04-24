// Loose tests that can't distinguish strict equality or nullish
// coalescing from their looser counterparts, so strict_equality and
// nullish_to_logical_or mutants survive.
import { isExact, pickDefault } from "./ops.ts";

let failed = 0;
const check = (got, want, name) => {
  if (got !== want) {
    console.error(`${name}: got ${got}, want ${want}`);
    failed++;
  }
};

// Equal string inputs — no strict-vs-loose distinction.
check(isExact("x", "x"), true, "strings equal");
check(isExact("x", "y"), false, "strings unequal");

// Non-zero default with a null input — ?? and || behave the same.
check(pickDefault(null), 42, "null -> 42");
check(pickDefault(100), 100, "100 passthrough");

if (failed > 0) process.exit(1);
console.log("PASS");
