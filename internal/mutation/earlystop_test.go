package mutation

import (
	"math"
	"testing"

	"github.com/0xPolygon/diffguard/internal/lang"
)

func TestZScore(t *testing.T) {
	for confidence, want := range map[float64]float64{0.95: 1.95996, 0.99: 2.57583} {
		if got := zScore(confidence); math.Abs(got-want) > 1e-4 {
			t.Errorf("zScore(%v) = %v, want %v", confidence, got, want)
		}
	}
}

func TestWilson(t *testing.T) {
	lo, hi := wilson(0, 0, 1.96)
	if lo != 0 || hi != 1 {
		t.Errorf("no trials: got [%v, %v], want [0, 1]", lo, hi)
	}
	// Reference values for 45/50 at 95%.
	lo, hi = wilson(45, 50, 1.959964)
	if math.Abs(lo-0.7864) > 1e-3 || math.Abs(hi-0.9565) > 1e-3 {
		t.Errorf("45/50: got [%.4f, %.4f], want [0.7864, 0.9565]", lo, hi)
	}
}

func TestNewEarlyStop_DisabledOutsideOpenInterval(t *testing.T) {
	for _, c := range []float64{0, -1, 1, 2} {
		if newEarlyStop(Options{Confidence: c}) != nil {
			t.Errorf("confidence %v should disable early stopping", c)
		}
	}
	var e *earlyStop
	e.observe(TierLogic, lang.OutcomeKilled)
	if e.isSettled(TierLogic) {
		t.Error("a disabled early stop never settles")
	}
}

func observeN(e *earlyStop, tier Tier, o lang.MutantOutcome, n int) {
	for i := 0; i < n; i++ {
		e.observe(tier, o)
	}
}

func TestEarlyStop_SettlesWhenClearlyPassing(t *testing.T) {
	e := newEarlyStop(Options{Confidence: 0.95, Tier1Threshold: 80})
	observeN(e, TierLogic, lang.OutcomeKilled, earlyStopMinSamples-1)
	if e.isSettled(TierLogic) {
		t.Fatal("must not settle below the minimum sample size, even at 100%")
	}
	e.observe(TierLogic, lang.OutcomeTimedOut)
	if !e.isSettled(TierLogic) {
		t.Fatal("30/30 killed is clearly above an 80% threshold")
	}
	if e.isSettled(TierSemantic) {
		t.Error("tiers settle independently")
	}
}

func TestEarlyStop_SettlesWhenClearlyFailing(t *testing.T) {
	e := newEarlyStop(Options{Confidence: 0.95, Tier1Threshold: 80})
	observeN(e, TierLogic, lang.OutcomeSurvived, 15)
	observeN(e, TierLogic, lang.OutcomeNotCovered, 15)
	if !e.isSettled(TierLogic) {
		t.Error("0/30 killed is clearly below an 80% threshold")
	}
}

func TestEarlyStop_StaysOpenNearThreshold(t *testing.T) {
	e := newEarlyStop(Options{Confidence: 0.95, Tier1Threshold: 80})
	for i := 0; i < 8; i++ {
		observeN(e, TierLogic, lang.OutcomeKilled, 4)
		e.observe(TierLogic, lang.OutcomeSurvived)
	}
	if e.isSettled(TierLogic) {
		t.Error("32/40 = 80% sits on the threshold; the verdict is not settled")
	}
}

func TestEarlyStop_IgnoresUnscoredOutcomesAndUngatedTier(t *testing.T) {
	e := newEarlyStop(Options{Confidence: 0.95, Tier1Threshold: 80})
	observeN(e, TierLogic, lang.OutcomeBuildFailed, 100)
	observeN(e, TierObservability, lang.OutcomeKilled, 100)
	if e.isSettled(TierLogic) || e.isSettled(TierObservability) {
		t.Error("excluded outcomes carry no evidence, and tier 3 is never gated")
	}
	if e.tiers[TierLogic].scored != 0 {
		t.Errorf("scored = %d, want 0", e.tiers[TierLogic].scored)
	}
}

func TestEarlyStop_UsesTier2Threshold(t *testing.T) {
	e := newEarlyStop(Options{Confidence: 0.95, Tier1Threshold: 99, Tier2Threshold: 50})
	observeN(e, TierSemantic, lang.OutcomeKilled, 30)
	if !e.isSettled(TierSemantic) {
		t.Error("30/30 clears a 50% tier-2 threshold")
	}
}
