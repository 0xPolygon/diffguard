// Seeded: nested match + if-let + guarded arms drive cognitive complexity
// well above the default 10 threshold. The expected finding pins the
// function name `tangled`.

pub fn tangled(x: Option<i32>, y: Option<i32>, flag: bool) -> i32 {
    let mut total = 0;
    if let Some(a) = x {
        if a > 0 && flag {
            if let Some(b) = y {
                match b {
                    v if v > 100 && a < 10 => total += v + a,
                    v if v < 0 || a == 0 => total -= v,
                    v if v == 0 => total = 0,
                    _ => total += 1,
                }
            } else if a > 5 || flag {
                total += a;
            }
        } else {
            match a {
                1 => total = 1,
                2 => total = 2,
                3 => total = 3,
                _ => total = -1,
            }
        }
    }
    total
}
