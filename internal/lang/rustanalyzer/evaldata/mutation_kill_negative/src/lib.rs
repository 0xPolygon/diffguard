// Same classify() as mutation_kill_positive, but tests only cover a
// single branch so most Tier-1 mutants survive.

pub fn classify(x: i32) -> i32 {
    if x > 0 {
        1
    } else if x < 0 {
        -1
    } else {
        0
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn one_positive_case_only() {
        // Covers only the positive branch; boundary, sign, and zero
        // cases are untested so mutants survive.
        assert_eq!(classify(5), 1);
    }
}
