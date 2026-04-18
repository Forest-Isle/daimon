package agent

import (
	"sync"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func TestToolResultCache_HitAndMiss(t *testing.T) {
	c := NewToolResultCache()

	input := `{"path":"/tmp/file.txt"}`
	want := tool.Result{Output: "hello"}

	c.Put("file_read", input, want)

	// Same key → hit
	got, ok := c.Get("file_read", input)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Output != want.Output {
		t.Fatalf("output mismatch: got %q, want %q", got.Output, want.Output)
	}

	// Different input → miss
	_, ok = c.Get("file_read", `{"path":"/tmp/other.txt"}`)
	if ok {
		t.Fatal("expected cache miss for different input")
	}

	// Different tool → miss
	_, ok = c.Get("bash", input)
	if ok {
		t.Fatal("expected cache miss for different tool")
	}
}

func TestToolResultCache_InvalidateByPath(t *testing.T) {
	c := NewToolResultCache()

	inputA := `{"path":"/tmp/a.txt"}`
	inputB := `{"path":"/tmp/b.txt"}`
	c.Put("file_read", inputA, tool.Result{Output: "a"})
	c.Put("file_read", inputB, tool.Result{Output: "b"})

	// Invalidate only /tmp/a.txt
	c.InvalidatePath("/tmp/a.txt")

	if _, ok := c.Get("file_read", inputA); ok {
		t.Fatal("expected a.txt entry to be evicted")
	}
	if _, ok := c.Get("file_read", inputB); !ok {
		t.Fatal("expected b.txt entry to survive")
	}
}

func TestToolResultCache_Clear(t *testing.T) {
	c := NewToolResultCache()

	c.Put("file_read", `{"path":"/x"}`, tool.Result{Output: "x"})
	c.Clear()

	if _, ok := c.Get("file_read", `{"path":"/x"}`); ok {
		t.Fatal("expected miss after Clear")
	}
}

func TestToolResultCache_ConcurrentAccess(t *testing.T) {
	c := NewToolResultCache()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			input := `{"path":"/tmp/file.txt"}`
			c.Put("file_read", input, tool.Result{Output: "data"})
			c.Get("file_read", input)
			c.InvalidatePath("/tmp/file.txt")
		}(i)
	}
	wg.Wait()
}

func TestToolResultCache_ExtractMultiplePathKeys(t *testing.T) {
	c := NewToolResultCache()

	input := `{"file_path":"/a.txt","directory":"/dir"}`
	c.Put("file_read", input, tool.Result{Output: "ok"})

	// Invalidating either path should evict the entry
	c.InvalidatePath("/a.txt")
	if _, ok := c.Get("file_read", input); ok {
		t.Fatal("expected eviction via file_path key")
	}

	// Re-add and invalidate via directory key
	c.Put("file_read", input, tool.Result{Output: "ok"})
	c.InvalidatePath("/dir")
	if _, ok := c.Get("file_read", input); ok {
		t.Fatal("expected eviction via directory key")
	}
}
