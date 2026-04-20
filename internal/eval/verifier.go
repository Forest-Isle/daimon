package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func VerifyReference(task TaskCase, agentOutput string) *VerifyResult {
	if task.Reference == nil {
		return &VerifyResult{Passed: true, Score: 1.0}
	}

	ref := task.Reference
	var checks []CheckResult

	if ref.Answer != "" {
		passed := strings.Contains(agentOutput, ref.Answer)
		checks = append(checks, CheckResult{
			Name:   "answer_contains",
			Passed: passed,
			Detail: fmt.Sprintf("looking for %q in output", ref.Answer),
		})
	}

	for _, s := range ref.MustContain {
		passed := strings.Contains(agentOutput, s)
		detail := fmt.Sprintf("found %q", s)
		if !passed {
			detail = fmt.Sprintf("%q not found in output", s)
		}
		checks = append(checks, CheckResult{
			Name:   "must_contain:" + s,
			Passed: passed,
			Detail: detail,
		})
	}

	for _, s := range ref.MustNotContain {
		passed := !strings.Contains(agentOutput, s)
		detail := "absent as expected"
		if !passed {
			detail = fmt.Sprintf("unwanted %q found in output", s)
		}
		checks = append(checks, CheckResult{
			Name:   "must_not_contain:" + s,
			Passed: passed,
			Detail: detail,
		})
	}

	for _, fc := range ref.FileChecks {
		checks = append(checks, verifyFileCheck(fc)...)
	}

	if ref.ExitCode != nil {
		checks = append(checks, verifyExitCode(*ref.ExitCode, agentOutput))
	}

	if len(checks) == 0 {
		return &VerifyResult{Passed: true, Score: 1.0}
	}

	passedCount := 0
	for _, c := range checks {
		if c.Passed {
			passedCount++
		}
	}
	score := float64(passedCount) / float64(len(checks))
	allPassed := passedCount == len(checks)

	return &VerifyResult{
		Passed: allPassed,
		Checks: checks,
		Score:  score,
	}
}

func verifyFileCheck(fc FileCheck) []CheckResult {
	var checks []CheckResult

	info, err := os.Stat(fc.Path)
	exists := err == nil && !info.IsDir()

	if fc.MustExist {
		checks = append(checks, CheckResult{
			Name:   "file_exists:" + fc.Path,
			Passed: exists,
			Detail: fmt.Sprintf("exists=%v", exists),
		})
	}

	if exists && fc.Contains != "" {
		data, err := os.ReadFile(fc.Path)
		passed := err == nil && strings.Contains(string(data), fc.Contains)
		checks = append(checks, CheckResult{
			Name:   "file_contains:" + fc.Path,
			Passed: passed,
			Detail: fmt.Sprintf("looking for %q", fc.Contains),
		})
	}

	if exists && fc.NotContains != "" {
		data, err := os.ReadFile(fc.Path)
		passed := err == nil && !strings.Contains(string(data), fc.NotContains)
		checks = append(checks, CheckResult{
			Name:   "file_not_contains:" + fc.Path,
			Passed: passed,
			Detail: fmt.Sprintf("unwanted %q", fc.NotContains),
		})
	}

	return checks
}

func verifyExitCode(expected int, agentOutput string) CheckResult {
	var parsed struct {
		ExitCode *int `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(agentOutput), &parsed); err != nil || parsed.ExitCode == nil {
		return CheckResult{
			Name:   "exit_code",
			Passed: false,
			Detail: "could not parse exit_code from output",
		}
	}
	passed := *parsed.ExitCode == expected
	return CheckResult{
		Name:   "exit_code",
		Passed: passed,
		Detail: fmt.Sprintf("expected %d, got %d", expected, *parsed.ExitCode),
	}
}
