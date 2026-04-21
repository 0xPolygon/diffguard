// Tests specifically distinguish strict-vs-loose equality and
// nullish-vs-falsy defaults, so strict_equality and
// nullish_to_logical_or mutants are killed.
import { isExact, pickDefault } from "./ops.ts";

let failed = 0;
const check = (got, want, name) => {
  if (got !== want) {
    console.error(`${name}: got ${got}, want ${want}`);
    failed++;
  }
};

// strict_equality: 0 === false is false, but 0 == false is true.
check(isExact(0, false), false, "0 !== false");
check(isExact(null, undefined), false, "null !== undefined");
check(isExact(1, 1), true, "1 === 1");

// nullish_to_logical_or: 0 ?? 42 is 0, but 0 || 42 is 42.
check(pickDefault(0), 0, "0 ?? 42");
check(pickDefault(null), 42, "null ?? 42");

if (failed > 0) process.exit(1);
console.log("PASS");
