package heart

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FSSource watches configured directories and turns local file changes into
// heart events. Missing directories are logged and skipped so one bad path does
// not stop the source.
type FSSource struct {
	Dirs []string
}

func (f *FSSource) Name() string {
	return "fs"
}

func (f *FSSource) Run(ctx context.Context, emit func(Event) error) error {
	if len(f.Dirs) == 0 {
		<-ctx.Done()
		return ctx.Err()
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fs: create watcher: %w", err)
	}
	defer watcher.Close()

	added := 0
	for _, dir := range f.Dirs {
		if err := watcher.Add(dir); err != nil {
			slog.Warn("fs: watch dir failed", "dir", dir, "err", err)
			continue
		}
		added++
	}
	if added == 0 {
		slog.Warn("fs: no watch dirs added")
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			f.emitEventOps(event, emit)
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Warn("fs: watch error", "err", err)
		}
	}
}

func (f *FSSource) emitEventOps(event fsnotify.Event, emit func(Event) error) {
	for _, op := range []struct {
		flag   fsnotify.Op
		kind   string
		suffix string
	}{
		{flag: fsnotify.Create, kind: "fs.created", suffix: "created"},
		{flag: fsnotify.Write, kind: "fs.modified", suffix: "modified"},
		{flag: fsnotify.Remove, kind: "fs.removed", suffix: "removed"},
		{flag: fsnotify.Rename, kind: "fs.renamed", suffix: "renamed"},
	} {
		if !event.Op.Has(op.flag) {
			continue
		}
		payload, err := json.Marshal(struct {
			Path string `json:"path"`
			Op   string `json:"op"`
		}{
			Path: event.Name,
			Op:   op.suffix,
		})
		if err != nil {
			slog.Debug("fs: marshal event failed", "path", event.Name, "kind", op.kind, "err", err)
			continue
		}
		dedupKey := fmt.Sprintf("%s|%s|%d", event.Name, op.kind, time.Now().Unix())
		// A dropped fs event is self-healing: a later write emits again, and
		// same-second editor bursts collapse through the heart store dedup key.
		if err := emit(Event{Kind: op.kind, Payload: string(payload), DedupKey: dedupKey}); err != nil {
			slog.Debug("fs: emit event failed", "path", event.Name, "kind", op.kind, "err", err)
		}
	}
}
