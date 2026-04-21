// Same behavior as complexity_positive split into flat helpers. Each
// function stays well under the cognitive threshold.

pub fn positive(x: Option<i32>) -> i32 {
    x.unwrap_or(0)
}

pub fn doubled(x: Option<i32>) -> i32 {
    positive(x) * 2
}

pub fn classify(n: i32) -> i32 {
    if n > 0 { 1 } else if n < 0 { -1 } else { 0 }
}
