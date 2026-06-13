package action

import "testing"

// TestClassifyBashCommand_Adversarial pins the P1-C acceptance: morphed
// destructive commands must not slip through to Reversible. Every case here must
// classify Irreversible.
func TestClassifyBashCommand_Adversarial(t *testing.T) {
	irreversible := []struct {
		name string
		cmd  string
	}{
		{"plain rm -rf", "rm -rf /tmp/x"},
		{"single-quote split", "r''m -rf /tmp/x"},
		{"double-quote split", `r""m -rf /tmp/x`},
		{"absolute path rm", "/bin/rm file"},
		{"cmd subst command name", "$(echo rm) -rf x"},
		{"param expansion command name", "${CMD} -rf x"},
		{"backtick command name", "`echo rm` -rf x"},
		{"rm in && list", "echo hi && rm -rf /"},
		{"rm in pipeline", "ls | rm -rf x"},
		{"rm in subshell", "(cd /tmp && rm -rf x)"},
		{"sudo rm", "sudo rm -rf /"},
		{"env rm", "env FOO=bar rm x"},
		{"xargs rm", "find . | xargs rm -rf"},
		{"timeout sudo wrapped subst", "sudo $(echo rm) x"},
		{"dd to device", "dd if=/dev/zero of=/dev/sda"},
		{"mkfs", "mkfs.ext4 /dev/sdb"},
		{"shred", "shred -u secret"},
		{"redirect to device", "cat foo > /dev/sda"},
		{"append to device", "echo x >> /dev/disk0"},
		{"clobber to device", "echo x >| /dev/sdb"},
		{"redirect to non-static target", "cat foo > $TARGET"},
		{"git force push long", "git push --force origin main"},
		{"git force push short", "git push -f"},
		{"git force push combined short", "git push -vf origin x"},
		{"git force-with-lease", "git push --force-with-lease origin main"},
		{"fork bomb", ":(){ :|:& };:"},
		{"unparseable (unbalanced quote)", `rm "unterminated`},
		{"wipefs", "wipefs -a /dev/sdc"},
		{"fdisk", "fdisk /dev/sda"},
		{"truncate to zero", "truncate -s 0 important.db"},
		{"eval quoted rm", `eval "rm -rf /"`},
		{"eval unquoted rm", "eval rm -rf /"},
		{"eval non-static", `eval "$CMD"`},
		{"bash -c rm", `bash -c "rm -rf /"`},
		{"sh -c rm", `sh -c "rm -rf /"`},
		{"bash combined -lc rm", `bash -lc "rm -rf /"`},
		{"bash -c non-static", `bash -c "$CMD"`},
		{"sudo bash -c rm", `sudo bash -c "rm -rf /"`},
		{"tee to device", "tee /dev/sda"},
		{"cp to device", "cp file /dev/sda"},
		{"mv to device", "mv x /dev/disk0"},
		{"find -delete", "find . -name '*.log' -delete"},
		{"find -exec rm", `find . -type f -exec rm {} \;`},
		{"git reset hard", "git reset --hard HEAD~1"},
		{"git clean force", "git clean -fdx"},
		{"git clean long force", "git clean --force"},
		{"rsync delete", "rsync -a --delete src/ dst/"},
		{"rsync del short", "rsync --del a b"},
	}
	for _, tc := range irreversible {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyBashCommand(tc.cmd); got != Irreversible {
				t.Fatalf("classifyBashCommand(%q) = %v, want Irreversible", tc.cmd, got)
			}
		})
	}
}

// TestClassifyBashCommand_Benign guards against over-classification: ordinary
// commands must stay Reversible so they can earn autonomy from a clean run.
func TestClassifyBashCommand_Benign(t *testing.T) {
	reversible := []struct {
		name string
		cmd  string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"ls", "ls -la"},
		{"echo to dev null", "echo hi > /dev/null"},
		{"echo to stderr", "echo err >&2"},
		{"redirect to file", "echo hi > out.txt"},
		{"git status", "git status"},
		{"git push no force", "git push origin main"},
		{"grep pipeline", "cat foo | grep bar"},
		{"go test", "go test ./..."},
		{"cd and build", "cd src && make build"},
		{"assignment only", "FOO=bar"},
		{"sudo apt update", "sudo apt-get update"},
		{"timeout ls", "timeout 5 ls"},
		{"file named rm-ish", "ls rm.txt"},
		{"mkdir", "mkdir -p a/b/c"},
		{"cp", "cp a b"},
		{"mv (rename, recoverable enough for trust)", "mv a b"},
		{"bash -c benign", `bash -c "ls -la"`},
		{"bash external script (out of static scope)", "bash deploy.sh"},
		{"eval benign", `eval "echo hi"`},
		{"tee to file", "tee output.log"},
		{"cp to dev null path-like", "cp a /tmp/devnull"},
		{"git reset soft", "git reset --soft HEAD~1"},
		{"git reset mixed", "git reset HEAD file.go"},
		{"git clean dry run", "git clean -n"},
		{"find no delete", "find . -name '*.go'"},
		{"rsync no delete", "rsync -a src/ dst/"},
	}
	for _, tc := range reversible {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyBashCommand(tc.cmd); got != Reversible {
				t.Fatalf("classifyBashCommand(%q) = %v, want Reversible", tc.cmd, got)
			}
		})
	}
}
