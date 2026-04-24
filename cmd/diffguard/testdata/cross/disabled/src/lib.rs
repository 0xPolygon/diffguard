// One function has a mutator-disable-func annotation; the other does
// not. The eval asserts that under --mutation-workers 4 no mutants are
// generated for `disabled_fn`, while `live_fn` produces normal mutants.

// mutator-disable-func
pub fn disabled_fn(x: i32) -> i32 {
    if x > 0 {
        x + 1
    } else {
        x - 1
    }
}

pub fn live_fn(x: i32) -> i32 {
    if x > 0 {
        x + 1
    } else {
        x - 1
    }
}
