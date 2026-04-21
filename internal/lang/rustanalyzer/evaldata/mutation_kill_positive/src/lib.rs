// Tested arithmetic function with boundary + sign coverage in the inline
// test module, so mutation operators (conditional_boundary,
// negate_conditional, math_operator, return_value) are killed.

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
    fn positive_returns_one() {
        assert_eq!(classify(5), 1);
    }

    #[test]
    fn negative_returns_minus_one() {
        assert_eq!(classify(-5), -1);
    }

    #[test]
    fn zero_returns_zero() {
        assert_eq!(classify(0), 0);
    }

    #[test]
    fn boundary_one_is_positive() {
        assert_eq!(classify(1), 1);
    }

    #[test]
    fn boundary_minus_one_is_negative() {
        assert_eq!(classify(-1), -1);
    }
}
