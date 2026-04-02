package hook

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

// WorkdirContextInjector is an OnUserMessage handler that injects the current
// working directory and optionally a file listing into the system prompt.
type WorkdirContextInjector struct {
	IncludeLS bool
	MaxFiles  int
}

// NewWorkdirContextInjector creates a workdir context injector.
func NewWorkdirContextInjector(config map[string]any) *WorkdirContextInjector {
	w := &WorkdirContextInjector{
		IncludeLS: false,
		MaxFiles:  20,
	}
	if v, ok := config["include_ls"]; ok {
		if b, ok := v.(bool); ok {
			w.IncludeLS = b
		}
	}
	if v, ok := config["max_files"]; ok {
		switch val := v.(type) {
		case int:
			w.MaxFiles = val
		case float64:
			w.MaxFiles = int(val)
		}
	}
	return w
}

func (w *WorkdirContextInjector) OnUserMessage(_ context.Context, _ OnUserMessageEvent) (OnUserMessageResult, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return OnUserMessageResult{}, nil
	}

	parts := []string{"CWD: " + cwd}

	if w.IncludeLS {
		entries, err := os.ReadDir(cwd)
		if err == nil {
			// Sort by modification time (most recent first)
			type fileEntry struct {
				name    string
				modTime int64
				isDir   bool
			}
			var files []fileEntry
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					continue
				}
				files = append(files, fileEntry{
					name:    e.Name(),
					modTime: info.ModTime().Unix(),
					isDir:   e.IsDir(),
				})
			}
			sort.Slice(files, func(i, j int) bool {
				return files[i].modTime > files[j].modTime
			})

			limit := w.MaxFiles
			if limit > len(files) {
				limit = len(files)
			}

			var listing []string
			for _, f := range files[:limit] {
				prefix := "  "
				if f.isDir {
					prefix = "d "
				}
				listing = append(listing, prefix+f.name)
			}
			if len(listing) > 0 {
				parts = append(parts, "Files:\n"+strings.Join(listing, "\n"))
			}
			if len(files) > limit {
				parts = append(parts, fmt.Sprintf("(%d more files not shown)", len(files)-limit))
			}
		}
	}

	return OnUserMessageResult{
		InjectedContext: []string{strings.Join(parts, "\n")},
	}, nil
}
