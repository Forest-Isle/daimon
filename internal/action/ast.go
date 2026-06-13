package action

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// maxClassifyDepth bounds recursion into nested interpreters (eval "...",
// bash -c "..."). A command obfuscated past this depth is too convoluted to
// prove benign, so it is treated as irreversible.
const maxClassifyDepth = 8

// classifyBashCommand assigns a reversibility Class to a shell command by parsing
// it into an AST, rather than substring-matching the raw text. The AST defeats
// the morphing tricks a substring blacklist misses: quote splitting (r''m),
// command substitution in command position ($(echo rm)), redirections to raw
// devices (>/dev/sda), exec wrappers (sudo rm), and nested interpreters
// (eval "rm -rf /", bash -c "rm -rf /").
//
// It is conservative by construction — it answers "could this be irreversible?"
// not "is this safe?". A command it cannot statically prove benign (a parse
// failure, or a command name built from an expansion it cannot evaluate) is
// classified Irreversible. This feeds the trust record and undo decision; the
// permission engine remains the real gate.
//
// Boundary: it analyses the command text it is given. An interpreter running an
// external file (bash script.sh, python evil.py) is opaque to static analysis;
// such cases rely on the permission engine and the execution sandbox, not this
// classifier.
func classifyBashCommand(command string) Class {
	if classifyBashAtDepth(command, 0) {
		return Irreversible
	}
	return Reversible
}

// classifyBashAtDepth parses one command string and reports whether it is
// irreversible, recursing up to maxClassifyDepth for nested interpreters.
func classifyBashAtDepth(command string, depth int) bool {
	if strings.TrimSpace(command) == "" {
		return false
	}
	if depth > maxClassifyDepth {
		return true
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return true // unparseable input cannot be analysed; assume the worst
	}

	irreversible := false
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			if callIsIrreversible(n, depth) {
				irreversible = true
			}
		case *syntax.Redirect:
			if redirectIsIrreversible(n) {
				irreversible = true
			}
		case *syntax.FuncDecl:
			// `:(){ :|:& };:` — a function named ":" is the classic fork bomb;
			// there is no legitimate reason to define one.
			if n.Name != nil && n.Name.Value == ":" {
				irreversible = true
			}
		}
		return !irreversible // stop walking once anything is flagged
	})
	return irreversible
}

// destructiveCommands are command names whose normal operation destroys data or
// devices. Matched on the base name, so /bin/rm and rm both hit.
var destructiveCommands = map[string]bool{
	"rm":       true,
	"rmdir":    true,
	"dd":       true,
	"shred":    true,
	"truncate": true,
	"fdisk":    true,
	"sfdisk":   true,
	"sgdisk":   true,
	"parted":   true,
	"wipefs":   true,
	"mke2fs":   true,
	"format":   true,
}

// execWrappers run a command supplied as an argument rather than doing the work
// themselves, so the destructive command hides one level down.
var execWrappers = map[string]bool{
	"sudo":    true,
	"doas":    true,
	"env":     true,
	"command": true,
	"exec":    true,
	"nohup":   true,
	"nice":    true,
	"ionice":  true,
	"time":    true,
	"timeout": true,
	"watch":   true,
	"setsid":  true,
	"stdbuf":  true,
	"xargs":   true,
}

// interpreters re-parse a script string passed via -c; the dangerous command
// lives inside that string.
var interpreters = map[string]bool{
	"sh":   true,
	"bash": true,
	"zsh":  true,
	"dash": true,
	"ash":  true,
	"ksh":  true,
}

// deviceWriters take a destination path as a positional argument (not a shell
// redirect), so writing to a raw device slips past redirect analysis.
var deviceWriters = map[string]bool{
	"tee": true,
	"cp":  true,
	"mv":  true,
	"ln":  true,
}

// callIsIrreversible inspects one command invocation. A command name that is not
// a static literal (built from a substitution or expansion) cannot be proven safe
// and is treated as irreversible; a static name is matched against the
// destructive set, force-push, device-writing positional args, exec wrappers, and
// nested interpreters/eval.
func callIsIrreversible(call *syntax.CallExpr, depth int) bool {
	if call == nil || len(call.Args) == 0 {
		return false // assignment-only (FOO=bar) or empty — nothing executed
	}
	name, static := wordLiteral(call.Args[0])
	if !static {
		// e.g. $(echo rm) or ${cmd} in command position: cannot evaluate, so we
		// cannot rule out a destructive expansion.
		return true
	}
	base := path.Base(name)
	args := call.Args[1:]

	if commandNameIsDestructive(base) {
		return true
	}
	if base == "git" && gitArgsForcePush(args) {
		return true
	}
	if deviceWriters[base] && anyArgIsRawDevice(args) {
		return true
	}
	if base == "eval" {
		return evalIsIrreversible(args, depth)
	}
	if interpreters[base] {
		return interpreterScriptIsIrreversible(args, depth)
	}
	if execWrappers[base] {
		return wrappedCommandIsIrreversible(args, depth)
	}
	return false
}

func commandNameIsDestructive(base string) bool {
	if strings.HasPrefix(base, "mkfs") { // mkfs, mkfs.ext4, mkfs.xfs, ...
		return true
	}
	return destructiveCommands[base]
}

// evalIsIrreversible joins eval's arguments back into a script string and
// re-classifies it. A non-static argument means the evaluated text depends on
// runtime state and cannot be proven safe.
func evalIsIrreversible(args []*syntax.Word, depth int) bool {
	var parts []string
	for _, w := range args {
		val, static := wordLiteral(w)
		if !static {
			return true
		}
		parts = append(parts, val)
	}
	return classifyBashAtDepth(strings.Join(parts, " "), depth+1)
}

// interpreterScriptIsIrreversible finds an interpreter's inline -c script and
// re-classifies it. The -c flag may be combined with other short flags (-lc).
// A non-static script is unprovable; an interpreter without -c is running an
// external file, which is out of static scope (returns false).
func interpreterScriptIsIrreversible(args []*syntax.Word, depth int) bool {
	for i, w := range args {
		flag, static := wordLiteral(w)
		if !static {
			continue
		}
		if isDashCFlag(flag) {
			if i+1 >= len(args) {
				return false // -c with no script: an error, nothing runs
			}
			script, scriptStatic := wordLiteral(args[i+1])
			if !scriptStatic {
				return true
			}
			return classifyBashAtDepth(script, depth+1)
		}
	}
	return false
}

// isDashCFlag reports whether a short-flag token requests command mode (-c),
// possibly bundled with other single-letter flags (-lc, -ec).
func isDashCFlag(flag string) bool {
	if !strings.HasPrefix(flag, "-") || strings.HasPrefix(flag, "--") {
		return false
	}
	return strings.Contains(flag, "c")
}

// wrappedCommandIsIrreversible analyses the command an exec wrapper (sudo, env,
// xargs, timeout, ...) runs. The wrapper's own option arity varies, so rather
// than locate the exact command boundary it scans every argument: a non-static
// argument, a destructive command name appearing anywhere, or a nested
// interpreter/eval all make the invocation irreversible.
func wrappedCommandIsIrreversible(args []*syntax.Word, depth int) bool {
	for i, w := range args {
		val, static := wordLiteral(w)
		if !static {
			return true
		}
		base := path.Base(val)
		switch {
		case commandNameIsDestructive(base):
			return true
		case base == "eval":
			if evalIsIrreversible(args[i+1:], depth) {
				return true
			}
		case interpreters[base]:
			if interpreterScriptIsIrreversible(args[i+1:], depth) {
				return true
			}
		}
	}
	return false
}

// gitArgsForcePush reports whether a `git` invocation is a force push, which
// rewrites remote history irrecoverably. Recognises `git push --force`,
// `git push -f`, `git push --force-with-lease`, combined short flags (-vf), and
// the global `-C <dir>` form since it scans all arguments.
func gitArgsForcePush(args []*syntax.Word) bool {
	sawPush := false
	force := false
	for _, w := range args {
		arg, static := wordLiteral(w)
		if !static {
			continue
		}
		switch {
		case arg == "push":
			sawPush = true
		case arg == "--force" || strings.HasPrefix(arg, "--force-with-lease"):
			force = true
		case strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "f"):
			force = true // combined short flags, e.g. -f or -vf
		}
	}
	return sawPush && force
}

// redirectIsIrreversible reports whether a redirection writes to a raw device.
// Writing to /dev/sda, /dev/disk0, etc. corrupts a block device; the standard
// character sinks (/dev/null, stdout, stderr, tty) are exempt. A write target
// that is not a static literal is treated as irreversible.
func redirectIsIrreversible(r *syntax.Redirect) bool {
	if r == nil || !isWriteRedirect(r.Op) || r.Word == nil {
		return false
	}
	target, static := wordLiteral(r.Word)
	if !static {
		return true
	}
	return isRawDevice(target)
}

func isWriteRedirect(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.ClbOut, syntax.RdrAll, syntax.AppAll:
		return true
	default:
		return false
	}
}

func anyArgIsRawDevice(args []*syntax.Word) bool {
	for _, w := range args {
		target, static := wordLiteral(w)
		if static && isRawDevice(target) {
			return true
		}
	}
	return false
}

var safeDevices = map[string]bool{
	"/dev/null":    true,
	"/dev/stdout":  true,
	"/dev/stderr":  true,
	"/dev/stdin":   true,
	"/dev/tty":     true,
	"/dev/zero":    true,
	"/dev/random":  true,
	"/dev/urandom": true,
}

func isRawDevice(target string) bool {
	clean := path.Clean(target)
	if !strings.HasPrefix(clean, "/dev/") {
		return false
	}
	if safeDevices[clean] {
		return false
	}
	// /dev/fd/N is process file descriptors, not a block device.
	if strings.HasPrefix(clean, "/dev/fd/") {
		return false
	}
	return true
}

// wordLiteral returns the static string value of a shell word and whether it is
// fully static. Adjacent literal and single-quoted parts concatenate (so r''m is
// the literal "rm"); any expansion, command substitution, or arithmetic makes the
// word non-static (ok=false), because its value depends on runtime state.
func wordLiteral(w *syntax.Word) (string, bool) {
	if w == nil {
		return "", false
	}
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			s, ok := dblQuotedLiteral(p)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		default:
			// ParamExp, CmdSubst, ArithmExp, ProcSubst, ExtGlob: not static.
			return "", false
		}
	}
	return b.String(), true
}

func dblQuotedLiteral(d *syntax.DblQuoted) (string, bool) {
	var b strings.Builder
	for _, part := range d.Parts {
		lit, ok := part.(*syntax.Lit)
		if !ok {
			return "", false // an expansion inside the quotes
		}
		b.WriteString(lit.Value)
	}
	return b.String(), true
}
