// Same overall behavior as sizes_positive, refactored across helpers so
// no single function exceeds the 50-line threshold.

pub fn step_one(x: i32) -> i32 { x + 1 }
pub fn step_two(x: i32) -> i32 { step_one(x) + 1 }
pub fn step_three(x: i32) -> i32 { step_two(x) + 1 }

pub fn short_func(input: i32) -> i32 {
    let a = step_one(input);
    let b = step_two(a);
    let c = step_three(b);
    c
}
