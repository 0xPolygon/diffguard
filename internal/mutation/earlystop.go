package mutation

import (
	"math"
	"sync"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// earlyStopMinSamples is the fewest scored mutants a tier needs before its
// verdict can be called early; below it the interval is too wide to trust.
const earlyStopMinSamples = 30

// earlyStop decides, per gating tier, when enough mutants have been tested
// that the kill rate is above or below the tier's threshold with the
// configured confidence. A nil *earlyStop never stops anything.
type earlyStop struct {
	mu    sync.Mutex
	z     float64
	tiers map[Tier]*tierTally
}

// tierTally is the running evidence for one gating tier.
type tierTally struct {
	threshold float64
	scored    int
	killed    int
	settled   bool
}

func newEarlyStop(opts Options) *earlyStop {
	if opts.Confidence <= 0 || opts.Confidence >= 1 {
		return nil
	}
	e := &earlyStop{z: zScore(opts.Confidence), tiers: map[Tier]*tierTally{}}
	for _, t := range []Tier{TierLogic, TierSemantic, TierObservability} {
		if threshold, gated := gatingThreshold(t, opts); gated {
			e.tiers[t] = &tierTally{threshold: threshold}
		}
	}
	return e
}

// observe records one result and settles the tier once the Wilson interval
// of its kill rate no longer straddles the threshold.
func (e *earlyStop) observe(t Tier, o lang.MutantOutcome) {
	if e == nil || (!isKill(o) && !isSurvivor(o)) {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	tally := e.tiers[t]
	if tally == nil {
		return
	}
	tally.scored++
	if isKill(o) {
		tally.killed++
	}
	if tally.scored < earlyStopMinSamples {
		return
	}
	lo, hi := wilson(tally.killed, tally.scored, e.z)
	// mutator-disable-next-line (a bound exactly on the threshold has measure zero)
	if lo*100 >= tally.threshold || hi*100 < tally.threshold {
		tally.settled = true
	}
}

func (e *earlyStop) isSettled(t Tier) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	tally := e.tiers[t]
	return tally != nil && tally.settled
}

// wilson returns the Wilson score interval for k successes in n trials.
func wilson(k, n int, z float64) (lo, hi float64) {
	if n == 0 {
		return 0, 1
	}
	p := float64(k) / float64(n)
	nf := float64(n)
	denom := 1 + z*z/nf
	center := (p + z*z/(2*nf)) / denom
	half := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / denom
	return center - half, center + half
}

// zScore returns the two-sided normal quantile for the confidence level,
// found by bisection on the error function.
func zScore(confidence float64) float64 {
	lo, hi := 0.0, 10.0
	for range 60 {
		mid := (lo + hi) / 2
		// mutator-disable-next-line (the quantile is a limit; < and <= converge to it alike)
		if math.Erf(mid/math.Sqrt2) < confidence {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}
