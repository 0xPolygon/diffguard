package mixedrepo

// BadGo is deliberately gnarly: deep nesting, many conditionals, logical
// chains. The fixture's sole job is to produce a complexity > 10 so the
// end-to-end test sees a Go section with a FAIL finding.
func BadGo(a, b, c, d, e int) int {
	total := 0
	if a > 0 && b > 0 {
		if c > 0 || d > 0 {
			for i := 0; i < a; i++ {
				if i%2 == 0 && e > 0 {
					total += i
				} else if i%3 == 0 || e < 0 {
					total -= i
				}
			}
		}
	}
	switch {
	case a == 1:
		total += 1
	case a == 2:
		total += 2
	case a == 3:
		total += 3
	default:
		total += 0
	}
	return total
}
