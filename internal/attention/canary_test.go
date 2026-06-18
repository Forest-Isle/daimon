package attention

import "testing"

func TestScreenRulesRejectsWakeUserDowngrade(t *testing.T) {
	corpus := []CanaryEvent{{Source: "mail", Kind: "mail.received", GroundTruth: WakeUser}}
	safe, rejected := ScreenRules(corpus, []Rule{{Source: "mail", Kind: "mail.received", Action: "cognize"}})
	if len(safe) != 0 || len(rejected) != 1 {
		t.Fatalf("wake downgrade must be rejected, safe=%+v rejected=%+v", safe, rejected)
	}
	if rejected[0].RuleAction != Cognize || rejected[0].GroundTruth != WakeUser {
		t.Fatalf("rejection details wrong: %+v", rejected[0])
	}
}

func TestScreenRulesKeepsCorrectionMatch(t *testing.T) {
	corpus := []CanaryEvent{{Source: "mail", Kind: "mail.received", Payload: "newsletter", GroundTruth: Ignore}}
	candidate := Rule{Source: "mail", Kind: "mail.received", Contains: "newsletter", Action: "ignore"}
	safe, rejected := ScreenRules(corpus, []Rule{candidate})
	if len(safe) != 1 || len(rejected) != 0 {
		t.Fatalf("matching corrected ground truth should be safe, safe=%+v rejected=%+v", safe, rejected)
	}
}

func TestScreenRulesKeepsUnmatchedCandidate(t *testing.T) {
	corpus := []CanaryEvent{{Source: "mail", Kind: "mail.received", GroundTruth: WakeUser}}
	candidate := Rule{Source: "calendar", Kind: "event.created", Action: "ignore"}
	safe, rejected := ScreenRules(corpus, []Rule{candidate})
	if len(safe) != 1 || len(rejected) != 0 {
		t.Fatalf("unmatched candidate should be safe, safe=%+v rejected=%+v", safe, rejected)
	}
}

func TestScreenRulesRejectsIgnoreForWakeUser(t *testing.T) {
	corpus := []CanaryEvent{{Source: "security", Kind: "alert", GroundTruth: WakeUser}}
	safe, rejected := ScreenRules(corpus, []Rule{{Source: "security", Kind: "alert", Action: "ignore"}})
	if len(safe) != 0 || len(rejected) != 1 {
		t.Fatalf("ignore below wake_user must be rejected, safe=%+v rejected=%+v", safe, rejected)
	}
}

func TestScreenRulesRejectsUnparseableAction(t *testing.T) {
	safe, rejected := ScreenRules(nil, []Rule{{Source: "mail", Kind: "mail.received", Action: "bogus"}})
	if len(safe) != 0 || len(rejected) != 1 {
		t.Fatalf("unparseable action must be rejected, safe=%+v rejected=%+v", safe, rejected)
	}
}

func TestScreenRulesMixedCandidates(t *testing.T) {
	corpus := []CanaryEvent{{Source: "mail", Kind: "mail.received", GroundTruth: Cognize}}
	safeRule := Rule{Source: "calendar", Kind: "event.created", Action: "ignore"}
	badRule := Rule{Source: "mail", Kind: "mail.received", Action: "ignore"}
	safe, rejected := ScreenRules(corpus, []Rule{safeRule, badRule})
	if len(safe) != 1 || safe[0] != safeRule || len(rejected) != 1 || rejected[0].Rule != badRule {
		t.Fatalf("mixed screen wrong, safe=%+v rejected=%+v", safe, rejected)
	}
}

func TestRuleMatchesWildcardsAndContains(t *testing.T) {
	if !ruleMatches(Rule{Kind: "message"}, "telegram", "message", "hello") {
		t.Fatal("empty source should wildcard")
	}
	if !ruleMatches(Rule{Source: "telegram"}, "telegram", "message", "hello") {
		t.Fatal("empty kind should wildcard")
	}
	if !ruleMatches(Rule{Source: "telegram", Kind: "message", Contains: "ell"}, "telegram", "message", "hello") {
		t.Fatal("contains substring should match")
	}
	if ruleMatches(Rule{Source: "mail"}, "telegram", "message", "hello") {
		t.Fatal("nonmatching source should not match")
	}
	if ruleMatches(Rule{Kind: "mail.received"}, "telegram", "message", "hello") {
		t.Fatal("nonmatching kind should not match")
	}
	if ruleMatches(Rule{Contains: "bye"}, "telegram", "message", "hello") {
		t.Fatal("nonmatching contains should not match")
	}
}
