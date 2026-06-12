Implemented the P0 rename task and wrote the required report to [output-p0-rename.md](/Users/wuqisen/dev/IronClaw/output-p0-rename.md).

Verification passed in order using local caches because the sandbox blocked global Go cache writes:

```text
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache make build-bin
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache make vet
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache make test-short
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache make test
grep -rn 'Forest-Isle/IronClaw' --include='*.go' . | wc -l  # 0
```

No git staging, commits, checkout, or restore operations were performed.