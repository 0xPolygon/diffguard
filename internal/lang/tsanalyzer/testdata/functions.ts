// Fixture: a small TypeScript file covering every function form the
// extractor should handle: standalone `function`, class methods (including
// static + private), arrow functions assigned to const, function
// expressions assigned to const, and a nested arrow.

export function standalone(x: number): number {
    return x + 1;
}

export const arrowConst = (x: number): number => {
    return x * 2;
};

export const fnExpr = function (x: number): number {
    return x - 1;
};

export class Counter {
    private n: number;

    constructor() {
        this.n = 0;
    }

    public increment(): number {
        const nestedHelper = (x: number) => x + 1;
        this.n = nestedHelper(this.n);
        return this.n;
    }

    static make(): Counter {
        return new Counter();
    }

    private reset(): void {
        this.n = 0;
    }
}

export function* gen(): Generator<number> {
    yield 1;
    yield 2;
}
