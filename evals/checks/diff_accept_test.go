package checks

import "testing"

const modifyDiff = `diff --git a/internal/foo/foo.go b/internal/foo/foo.go
index 1111111..2222222 100644
--- a/internal/foo/foo.go
+++ b/internal/foo/foo.go
@@ -1,3 +1,3 @@
 package foo
-func A() int { return 1 }
+func A() int { return 2 }
diff --git a/internal/bar/bar.go b/internal/bar/bar.go
index 3333333..4444444 100644
--- a/internal/bar/bar.go
+++ b/internal/bar/bar.go
@@ -10,2 +10,3 @@ func B() {
 	x := 1
+	y := 2
 	_ = x
`

const addedFileDiff = `diff --git a/internal/new/new.go b/internal/new/new.go
new file mode 100644
index 0000000..5555555
--- /dev/null
+++ b/internal/new/new.go
@@ -0,0 +1,2 @@
+package new
+func C() {}
`

const deletedFileDiff = `diff --git a/internal/old/old.go b/internal/old/old.go
deleted file mode 100644
index 6666666..0000000
--- a/internal/old/old.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package old
-func D() {}
`

const renameDiff = `diff --git a/internal/a/x.go b/internal/b/x.go
similarity index 100%
rename from internal/a/x.go
rename to internal/b/x.go
`

const binaryDiff = `diff --git a/assets/logo.png b/assets/logo.png
index 7777777..8888888 100644
Binary files a/assets/logo.png and b/assets/logo.png differ
`

func TestParseUnifiedDiff_Modify(t *testing.T) {
	changes, err := ParseUnifiedDiff(modifyDiff)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("want 2 files, got %d", len(changes))
	}
	foo := changes[0]
	if foo.Path != "internal/foo/foo.go" || foo.Op != OpModified {
		t.Fatalf("foo path/op wrong: %+v", foo)
	}
	if len(foo.AddedLines) != 1 || foo.AddedLines[0] != "func A() int { return 2 }" {
		t.Fatalf("foo added lines wrong: %#v", foo.AddedLines)
	}
	if len(foo.RemovedLines) != 1 || foo.RemovedLines[0] != "func A() int { return 1 }" {
		t.Fatalf("foo removed lines wrong: %#v", foo.RemovedLines)
	}
	// The "+++ b/..." / "--- a/..." headers must not leak into body lines.
	for _, l := range append(foo.AddedLines, foo.RemovedLines...) {
		if l == " b/internal/foo/foo.go" || l == " a/internal/foo/foo.go" {
			t.Fatalf("header captured as body: %q", l)
		}
	}
}

func TestParseUnifiedDiff_Ops(t *testing.T) {
	added, _ := ParseUnifiedDiff(addedFileDiff)
	if len(added) != 1 || added[0].Op != OpAdded || added[0].Path != "internal/new/new.go" {
		t.Fatalf("added wrong: %+v", added)
	}
	deleted, _ := ParseUnifiedDiff(deletedFileDiff)
	if len(deleted) != 1 || deleted[0].Op != OpDeleted {
		t.Fatalf("deleted wrong: %+v", deleted)
	}
	renamed, _ := ParseUnifiedDiff(renameDiff)
	if len(renamed) != 1 || renamed[0].Op != OpRenamed {
		t.Fatalf("renamed wrong: %+v", renamed)
	}
	if renamed[0].OldPath != "internal/a/x.go" || renamed[0].Path != "internal/b/x.go" {
		t.Fatalf("rename paths wrong: %+v", renamed[0])
	}
	bin, _ := ParseUnifiedDiff(binaryDiff)
	if len(bin) != 1 || !bin[0].Binary {
		t.Fatalf("binary wrong: %+v", bin)
	}
}

func TestParseUnifiedDiff_Empty(t *testing.T) {
	changes, err := ParseUnifiedDiff("   \n")
	if err != nil || changes != nil {
		t.Fatalf("empty diff: changes=%v err=%v", changes, err)
	}
}

func TestEvalTestsGreen(t *testing.T) {
	tests := []struct {
		name string
		in   TestOutcome
		pass bool
	}{
		{"not run", TestOutcome{Ran: false}, false},
		{"green", TestOutcome{Ran: true, Success: true, Failed: 0}, true},
		{"failure flag", TestOutcome{Ran: true, Success: false}, false},
		{"failed count", TestOutcome{Ran: true, Success: false, Failed: 2}, false},
		{"success but failed>0", TestOutcome{Ran: true, Success: true, Failed: 1}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := evalTestsGreen(tc.in)
			if got.Pass != tc.pass {
				t.Fatalf("want pass=%v, got %+v", tc.pass, got)
			}
			if !got.Pass && got.Reason == "" {
				t.Fatalf("failing gate must have a reason")
			}
		})
	}
}

const deleteTestDiff = `diff --git a/internal/foo/foo_test.go b/internal/foo/foo_test.go
deleted file mode 100644
index 1111111..0000000
--- a/internal/foo/foo_test.go
+++ /dev/null
@@ -1,5 +0,0 @@
-package foo
-func TestA(t *testing.T) {
-	if A() != 1 { t.Fatal("bad") }
-}
`

const removeTestFuncDiff = `diff --git a/internal/foo/foo_test.go b/internal/foo/foo_test.go
index 1111111..2222222 100644
--- a/internal/foo/foo_test.go
+++ b/internal/foo/foo_test.go
@@ -1,6 +1,2 @@
 package foo
-func TestA(t *testing.T) {
-	if A() != 1 { t.Fatal("bad") }
-}
 func TestB(t *testing.T) {}
`

const addSkipDiff = `diff --git a/internal/foo/foo_test.go b/internal/foo/foo_test.go
index 1111111..2222222 100644
--- a/internal/foo/foo_test.go
+++ b/internal/foo/foo_test.go
@@ -1,3 +1,4 @@
 package foo
 func TestA(t *testing.T) {
+	t.Skip("flaky")
 	if A() != 1 { t.Fatal("bad") }
 }
`

const weakenAssertDiff = `diff --git a/internal/foo/foo_test.go b/internal/foo/foo_test.go
index 1111111..2222222 100644
--- a/internal/foo/foo_test.go
+++ b/internal/foo/foo_test.go
@@ -1,4 +1,3 @@
 package foo
 func TestA(t *testing.T) {
-	if A() != 1 { t.Fatal("bad") }
 }
`

const addTestDiff = `diff --git a/internal/foo/foo_test.go b/internal/foo/foo_test.go
index 1111111..2222222 100644
--- a/internal/foo/foo_test.go
+++ b/internal/foo/foo_test.go
@@ -1,2 +1,5 @@
 package foo
+func TestNew(t *testing.T) {
+	if A() != 2 { t.Fatal("bad") }
+}
`

const renameTestAwayDiff = `diff --git a/internal/foo/foo_test.go b/internal/foo/foo.go
similarity index 100%
rename from internal/foo/foo_test.go
rename to internal/foo/foo.go
`

const addBuildTagDiff = `diff --git a/internal/foo/foo_test.go b/internal/foo/foo_test.go
index 1111111..2222222 100644
--- a/internal/foo/foo_test.go
+++ b/internal/foo/foo_test.go
@@ -1,2 +1,4 @@
+//go:build never
+
 package foo
 func TestA(t *testing.T) {}
`

const helperSkipDiff = `diff --git a/internal/foo/foo_test.go b/internal/foo/foo_test.go
index 1111111..2222222 100644
--- a/internal/foo/foo_test.go
+++ b/internal/foo/foo_test.go
@@ -1,3 +1,4 @@
 package foo
 func TestA(t *testing.T) {
+	suite.Skip("disabled")
 }
`

func TestEvalNoTestTamper(t *testing.T) {
	tests := []struct {
		name string
		diff string
		pass bool
	}{
		{"delete test file", deleteTestDiff, false},
		{"remove test func", removeTestFuncDiff, false},
		{"add skip", addSkipDiff, false},
		{"weaken assertion", weakenAssertDiff, false},
		{"rename test away", renameTestAwayDiff, false},
		{"add build tag", addBuildTagDiff, false},
		{"non-t/b skip", helperSkipDiff, false},
		{"add test", addTestDiff, true},
		{"non-test file", modifyDiff, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			changes, err := ParseUnifiedDiff(tc.diff)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got := evalNoTestTamper(changes)
			if got.Pass != tc.pass {
				t.Fatalf("want pass=%v, got %+v", tc.pass, got)
			}
		})
	}
}

func TestEvalInScope(t *testing.T) {
	modify, _ := ParseUnifiedDiff(modifyDiff)
	rename, _ := ParseUnifiedDiff(renameDiff)

	if g := evalInScope(modify, nil); !g.Pass {
		t.Fatalf("empty allowlist should pass: %+v", g)
	}
	if g := evalInScope(modify, []string{"internal/*/*.go"}); !g.Pass {
		t.Fatalf("in-scope change should pass: %+v", g)
	}
	if g := evalInScope(modify, []string{"internal/foo/*.go"}); g.Pass {
		t.Fatalf("bar/ should be out of scope: %+v", g)
	}
	// Rename: destination allowed but source not → still out of scope.
	if g := evalInScope(rename, []string{"internal/b/*.go"}); g.Pass {
		t.Fatalf("rename source out of scope must fail: %+v", g)
	}
	// Malformed glob must be surfaced, not silently swallowed.
	if g := evalInScope(modify, []string{"internal/[a-.go"}); g.Pass {
		t.Fatalf("invalid glob must fail the gate: %+v", g)
	}
}

func TestEvaluateDiff_AllPass(t *testing.T) {
	v, err := EvaluateDiff(DiffAcceptInput{
		Diff:         addTestDiff,
		AllowedPaths: []string{"internal/foo/*.go"},
		Tests:        TestOutcome{Ran: true, Success: true, Failed: 0},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !v.Accept || len(v.Gates) != 3 {
		t.Fatalf("want accept with 3 gates, got %+v", v)
	}
}

func TestEvaluateDiff_RewardHackRejected(t *testing.T) {
	// A real-looking diff that deletes a test, yet the test run is "green"
	// (because the test no longer exists). tests-green passes but
	// no-test-tamper must reject.
	v, err := EvaluateDiff(DiffAcceptInput{
		Diff:  deleteTestDiff,
		Tests: TestOutcome{Ran: true, Success: true, Failed: 0},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v.Accept {
		t.Fatalf("reward-hack diff must be rejected: %+v", v)
	}
	if v.Gates[0].Pass != true || v.Gates[1].Pass != false {
		t.Fatalf("expected tests-green pass + no-test-tamper fail, got %+v", v.Gates)
	}
}

func TestEvaluateDiff_FailsClosedOnBadTests(t *testing.T) {
	v, _ := EvaluateDiff(DiffAcceptInput{
		Diff:  addTestDiff,
		Tests: TestOutcome{Ran: false},
	})
	if v.Accept {
		t.Fatalf("un-run tests must reject: %+v", v)
	}
}
