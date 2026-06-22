// Package calibration scores an LLM judge against human ground-truth labels. It
// answers Phase-1's second acceptance question — "is the judge any good?" — with
// numbers: a confusion matrix, TPR/TNR, raw agreement, Cohen's kappa, and a
// Wilson confidence interval on the agreement. Raw agreement alone is
// misleading under class imbalance (always guessing the majority class can hit
// 85%), so kappa is the de-noised bar.
package calibration

import (
	"fmt"
	"io"
	"math"
)

// Confusion is the 2x2 matrix with "pass" as the positive class: the judge and
// the human each label a sample pass (true) or fail (false).
type Confusion struct {
	TP int // human pass, judge pass
	TN int // human fail, judge fail
	FP int // human fail, judge pass (judge too lenient)
	FN int // human pass, judge fail (judge too strict)
}

// N is the total number of labelled samples.
func (c Confusion) N() int { return c.TP + c.TN + c.FP + c.FN }

// RawAgreement is the fraction of samples where judge and human agree.
func (c Confusion) RawAgreement() float64 {
	if c.N() == 0 {
		return 0
	}
	return float64(c.TP+c.TN) / float64(c.N())
}

// TPR (sensitivity) is the fraction of human-pass samples the judge calls pass.
func (c Confusion) TPR() float64 {
	d := c.TP + c.FN
	if d == 0 {
		return 0
	}
	return float64(c.TP) / float64(d)
}

// TNR (specificity) is the fraction of human-fail samples the judge calls fail.
func (c Confusion) TNR() float64 {
	d := c.TN + c.FP
	if d == 0 {
		return 0
	}
	return float64(c.TN) / float64(d)
}

// Kappa is Cohen's kappa: agreement after removing chance agreement. It is 0
// when chance agreement is total (a degenerate single-class set) unless the
// observed agreement is also perfect, in which case it is 1.
func (c Confusion) Kappa() float64 {
	n := float64(c.N())
	if n == 0 {
		return 0
	}
	po := c.RawAgreement()
	pHuman := float64(c.TP+c.FN) / n
	pJudge := float64(c.TP+c.FP) / n
	pe := pHuman*pJudge + (1-pHuman)*(1-pJudge)
	if 1-pe < 1e-12 {
		if po >= 1 {
			return 1
		}
		return 0
	}
	return (po - pe) / (1 - pe)
}

// Interval is a closed numeric interval, used for the agreement CI.
type Interval struct {
	Lo float64
	Hi float64
}

// WilsonInterval is the Wilson score interval for a binomial proportion
// (successes/n) at the given z (1.96 ≈ 95%). It is well-behaved at small n and
// near 0/1, unlike the normal approximation. n==0 returns the maximal [0,1].
func WilsonInterval(successes, n int, z float64) Interval {
	if n == 0 {
		return Interval{Lo: 0, Hi: 1}
	}
	nf := float64(n)
	p := float64(successes) / nf
	z2 := z * z
	denom := 1 + z2/nf
	center := (p + z2/(2*nf)) / denom
	margin := (z * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf))) / denom
	return Interval{Lo: clamp01(center - margin), Hi: clamp01(center + margin)}
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// Report is the full calibration result for a label set.
type Report struct {
	Confusion
	N            int
	RawAgreement float64
	TPR          float64
	TNR          float64
	Kappa        float64
	AgreementCI  Interval
}

// Analyze tabulates labels and computes every calibration metric at 95%.
func Analyze(labels []Label) Report {
	c := Tabulate(labels)
	return Report{
		Confusion:    c,
		N:            c.N(),
		RawAgreement: c.RawAgreement(),
		TPR:          c.TPR(),
		TNR:          c.TNR(),
		Kappa:        c.Kappa(),
		AgreementCI:  WilsonInterval(c.TP+c.TN, c.N(), 1.96),
	}
}

// Meets reports whether the judge clears a kappa bar (the de-noised agreement
// threshold). Use kappa, not raw agreement, as the trust gate.
func (r Report) Meets(kappaThreshold float64) bool {
	return r.Kappa >= kappaThreshold
}

// Render writes the calibration metrics to w.
func (r Report) Render(w io.Writer) {
	fmt.Fprintf(w, "samples         %d\n", r.N)
	fmt.Fprintf(w, "confusion       TP=%d TN=%d FP=%d FN=%d\n", r.TP, r.TN, r.FP, r.FN)
	fmt.Fprintf(w, "raw agreement   %.3f  (95%% CI %.3f–%.3f)\n", r.RawAgreement, r.AgreementCI.Lo, r.AgreementCI.Hi)
	fmt.Fprintf(w, "TPR / TNR       %.3f / %.3f\n", r.TPR, r.TNR)
	fmt.Fprintf(w, "Cohen's kappa   %.3f\n", r.Kappa)
}
