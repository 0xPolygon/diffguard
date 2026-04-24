// Negative: same behavior refactored across named exports. Nothing
// approaches the 50-line threshold.

export function stepOne(x: number): number { return x + 1; }
export function stepTwo(x: number): number { return stepOne(x) + 1; }
export function stepThree(x: number): number { return stepTwo(x) + 1; }

export const shortFunc = (input: number): number => {
  const a = stepOne(input);
  const b = stepTwo(a);
  const c = stepThree(b);
  return c;
};
