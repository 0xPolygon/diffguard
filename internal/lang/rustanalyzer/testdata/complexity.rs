// Fixture for the cognitive-complexity scorer. Each function below has a
// documented expected score so the test can assert precise numbers.

// Empty function: no control flow, score 0.
fn empty() {}

// Single if: +1 base, 0 nesting, 0 logical.
fn one_if(x: i32) -> i32 {
    if x > 0 {
        1
    } else {
        0
    }
}

// match with 3 arms, 2 guarded: +1 for match, +2 for guarded arms.
fn guarded(x: i32) -> i32 {
    match x {
        n if n > 0 => 1,
        n if n < 0 => -1,
        _ => 0,
    }
}

// Nested if inside for: for = +1, nested if = +1 base + 1 nesting = +2.
// Total = 3.
fn nested(xs: &[i32]) -> i32 {
    let mut n = 0;
    for x in xs {
        if *x > 0 {
            n += 1;
        }
    }
    n
}

// Logical chain: if +1, &&/|| switch counted. "a && b && c" is a single
// run = +1; "a && b || c" is two runs = +2. This fn has "a && b || c":
// base if = +1, logical = +2, total = 3.
fn logical(a: bool, b: bool, c: bool) -> bool {
    if a && b || c {
        true
    } else {
        false
    }
}

// unsafe block should NOT count; `?` should NOT count. This fn has:
// one if = +1, one ? = +0, one unsafe = +0. Total = 1.
fn unsafe_and_try(maybe: Option<i32>) -> Result<i32, ()> {
    let v = maybe.ok_or(())?;
    if v > 0 {
        return Ok(v);
    }
    unsafe {
        let _p: *const i32 = std::ptr::null();
    }
    Ok(0)
}
