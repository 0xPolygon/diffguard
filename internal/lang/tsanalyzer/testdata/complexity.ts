// Fixture for the TypeScript cognitive-complexity scorer. Each function
// below has a documented expected score so the test can assert precise
// numbers.

// Empty function: no control flow, score 0.
export function empty(): void {}

// Single if: +1 base, 0 nesting, 0 logical.
export function oneIf(x: number): number {
    if (x > 0) {
        return 1;
    }
    return 0;
}

// if/else: +1 for the if, +1 for the else branch = 2.
export function ifElse(x: number): number {
    if (x > 0) {
        return 1;
    } else {
        return 0;
    }
}

// switch with 3 cases (all with content) + default: +1 for the switch,
// +3 for non-empty cases. Default counts only if it has content — it does
// here, so +1. Total = 1 + 3 + 1 = 5.
export function sw(x: number): number {
    switch (x) {
        case 1: {
            return 10;
        }
        case 2: {
            return 20;
        }
        case 3: {
            return 30;
        }
        default: {
            return 0;
        }
    }
}

// try/catch: +1 for the try, +1 for the catch = 2.
export function tryCatch(): void {
    try {
        doSomething();
    } catch (e) {
        handle(e);
    }
}

// Ternary: +1. Nested ternary: +1 base + 1 nesting = 2 for the inner.
// Total = 1 + 2 = 3.
export function ternary(x: number): number {
    return x > 0 ? (x > 10 ? 100 : 50) : 0;
}

// Logical chain: if +1, && run = +1, then switch to || = +1. Total = 3.
export function logical(a: boolean, b: boolean, c: boolean): boolean {
    if (a && b || c) {
        return true;
    }
    return false;
}

// Optional chaining + nullish coalescing — MUST NOT count toward
// complexity per the spec. `await`/`async` alone also must not count.
// The only control flow is the `if`, so score = 1.
export async function notCounted(x: { v?: number } | null): Promise<number> {
    const val = x?.v ?? 0;
    if (val > 0) {
        await someAsync();
        return 1;
    }
    return 0;
}

// Promise-chain .catch: +1 per `.catch(...)` promise-chain call. No other
// control flow in the body, so score = 1.
export function promiseCatch(): Promise<number> {
    return someAsync().catch((e) => 0);
}

// Stub helpers so the fixture type-checks in users' IDEs. Tree-sitter
// doesn't care but keeping them real functions lets us verify they're
// treated as separate entries (complexity 0 for the declaration itself,
// but the calculator ignores them — their bodies contain no control flow).
function doSomething(): void {}
function handle(_e: unknown): void {}
function someAsync(): Promise<number> {
    return Promise.resolve(1);
}
