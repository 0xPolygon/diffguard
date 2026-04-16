// mutator-disable-func
export function disabledFn(x: number): number {
  if (x > 0) {
    return x + 1;
  }
  return x - 1;
}

export function liveFn(x: number): number {
  if (x > 0) {
    return x + 1;
  }
  return x - 1;
}
