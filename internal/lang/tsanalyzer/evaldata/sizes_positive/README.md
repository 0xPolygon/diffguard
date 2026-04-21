# sizes_positive

Seeded issue: `longFunc` is an arrow function assigned to a `const` with
~60 lines of body, exceeding the 50-line function threshold. Complexity
stays flat (no branches).

Expected verdict: Code Sizes FAIL with a finding on `longFunc`.
