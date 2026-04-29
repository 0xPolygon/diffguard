package mixedrepo

// Clean counterpart: trivial function, well under the complexity / size
// thresholds. Sole purpose is to give the Go analyzer a file to load so the
// detector-driven pipeline actually runs.
func GoodGo(x int) int {
	return x + 1
}
