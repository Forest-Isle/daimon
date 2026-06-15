package replay

import (
	"context"
	"time"
)

// This file is the immune system's verdict layer (blueprint §4.10 modes 2 & 3),
// built on the offline re-scorer in rescore.go. Two primitives:
//
//   - SelectRegression / Recent build the set of recorded sessions a change must
//     be judged against — the must-pass regression set (mode 2) and the recent
//     canary window (mode 3).
//   - Canary replays a candidate configuration over those sessions and returns a
//     single pass/fail verdict. It is the gate selfops and distill run before a
//     self-modification (a distilled skill, a prompt/rule edit, a model swap) is
//     allowed to graduate.
//
// Everything here is action dry-run: Canary only generates and judges text via
// the underlying Rescore, it never executes tools or writes the world.

// RegressionCriteria selects which recorded sessions belong in the must-pass
// regression set (blueprint §4.10 mode 2): "用户纠正过的情节自动入集……改动必须全过".
// A session enrolls when it was salvaged (an exit-contract failure the change
// must not reintroduce) or when it is named in CorrectionSessionIDs.
//
// Resolving a user correction to a session is the caller's job, not this
// package's: a journal correction carries an episode id, and mapping episode →
// session lives with the store that owns both. This package operates only on the
// Sessions it is handed, so it stays pure and testable; the caller passes the
// resolved session id set in.
type RegressionCriteria struct {
	// CorrectionSessionIDs is the set of session ids known to contain a corrected
	// episode. Nil/empty means no correction-based enrollment.
	CorrectionSessionIDs map[string]bool
	// IncludeSalvaged enrolls sessions whose episode had to be framework-salvaged.
	IncludeSalvaged bool
}

// SelectRegression returns the subset of sessions that belong in the regression
// set, preserving input order and never returning a session twice. A session
// with an empty id is never enrolled (it cannot be matched or de-duplicated
// reliably). When the criteria select nothing, the result is empty, not nil-safe
// in any special way — callers that gate on len==0 (Canary does) treat an empty
// set as "no evidence".
func SelectRegression(sessions []Session, c RegressionCriteria) []Session {
	out := make([]Session, 0, len(sessions))
	seen := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		if s.SessionID == "" || seen[s.SessionID] {
			continue
		}
		enroll := (c.IncludeSalvaged && s.Salvaged) ||
			(c.CorrectionSessionIDs != nil && c.CorrectionSessionIDs[s.SessionID])
		if enroll {
			seen[s.SessionID] = true
			out = append(out, s)
		}
	}
	return out
}

// Recent returns the last n sessions — the canary window (blueprint §4.10 mode 3:
// "先在最近 50 个情节上回放"). Sessions arrive in first-appearance (chronological)
// order from LoadDir, so the tail is the most recent. n<=0 or n>=len(sessions)
// returns all sessions. The returned slice shares backing storage with the input
// (it is a sub-slice), which is fine for the read-only re-scoring that follows.
func Recent(sessions []Session, n int) []Session {
	if n <= 0 || n >= len(sessions) {
		return sessions
	}
	return sessions[len(sessions)-n:]
}

// CanaryOptions parameterizes a canary run. Rescore carries the candidate model,
// per-call token bound, and exchange cap into the underlying re-scorer. MaxErrors
// is how many per-exchange judge/candidate failures are tolerated before the
// canary fails closed (default 0: a single unscored exchange sinks the run, since
// an exchange we could not score is an exchange we cannot certify).
type CanaryOptions struct {
	Rescore   RescoreOptions
	MaxErrors int
}

// CanaryReport is the verdict of replaying a candidate over a session set. It
// embeds the full RescoreReport (per-exchange detail and aggregates) and adds the
// single gate the caller acts on: Passed.
type CanaryReport struct {
	RescoreReport
	// Passed is the promotion gate. It is true only when the run produced real,
	// complete, regression-free evidence — see Canary for the exact predicate.
	Passed bool
	// Sessions is how many sessions the canary replayed over.
	Sessions int
}

// Canary replays a candidate configuration over the given sessions and reduces
// the re-scoring to one promotion verdict (blueprint §4.10 mode 3). It is the
// gate selfops/distill run before a self-modification graduates.
//
// It fails closed: a change earns promotion, it is never granted by default. The
// gate passes only when ALL of the following hold:
//
//   - Compared > 0      — at least one exchange was actually re-scored (an empty
//     session set, or a set with only tool-call/empty turns, is no evidence).
//   - Regressions == 0  — the judge flagged no candidate response as clearly worse.
//   - Errors <= MaxErrors — unscored exchanges (candidate/judge failures) stay
//     within tolerance; an exchange we could not score we cannot certify.
//   - !Capped           — coverage was complete; a run that hit MaxExchanges with
//     scorable exchanges left over certifies only part of the window, so it does
//     not pass. Size RescoreOptions.MaxExchanges to cover the window (or 0 for no
//     cap) when you intend a passing verdict to mean full coverage.
//
// Skipped exchanges (pure tool-call or empty-baseline turns) do not block a pass:
// they are deliberately out of scope for text re-scoring, not evidence against
// the change.
func Canary(ctx context.Context, sessions []Session, cand Candidate, judge Judge, opts CanaryOptions, now func() time.Time) (CanaryReport, error) {
	rep, err := Rescore(ctx, sessions, cand, judge, opts.Rescore, now)
	if err != nil {
		return CanaryReport{}, err
	}
	cr := CanaryReport{RescoreReport: rep, Sessions: len(sessions)}
	cr.Passed = rep.Compared > 0 &&
		rep.Regressions == 0 &&
		rep.Errors <= opts.MaxErrors &&
		!rep.Capped
	return cr, nil
}
