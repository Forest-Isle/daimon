package agent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestTopologicalSort_NoDeps(t *testing.T) {
	tasks := []AgentTask{
		{ID: "a", AgentName: "agent1", Task: "task a"},
		{ID: "b", AgentName: "agent2", Task: "task b"},
		{ID: "c", AgentName: "agent3", Task: "task c"},
	}
	layers, err := TopologicalSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	if len(layers[0]) != 3 {
		t.Fatalf("expected 3 tasks in layer 0, got %d", len(layers[0]))
	}
}

func TestTopologicalSort_LinearDeps(t *testing.T) {
	tasks := []AgentTask{
		{ID: "a", AgentName: "agent1", Task: "task a"},
		{ID: "b", AgentName: "agent2", Task: "task b", DependsOn: []string{"a"}},
		{ID: "c", AgentName: "agent3", Task: "task c", DependsOn: []string{"b"}},
	}
	layers, err := TopologicalSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("expected 3 layers, got %d", len(layers))
	}
	if layers[0][0].ID != "a" {
		t.Errorf("layer 0 should contain 'a', got %q", layers[0][0].ID)
	}
	if layers[1][0].ID != "b" {
		t.Errorf("layer 1 should contain 'b', got %q", layers[1][0].ID)
	}
	if layers[2][0].ID != "c" {
		t.Errorf("layer 2 should contain 'c', got %q", layers[2][0].ID)
	}
}

func TestTopologicalSort_DiamondDeps(t *testing.T) {
	tasks := []AgentTask{
		{ID: "a", AgentName: "agent1", Task: "task a"},
		{ID: "b", AgentName: "agent2", Task: "task b", DependsOn: []string{"a"}},
		{ID: "c", AgentName: "agent3", Task: "task c", DependsOn: []string{"a"}},
		{ID: "d", AgentName: "agent4", Task: "task d", DependsOn: []string{"b", "c"}},
	}
	layers, err := TopologicalSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("expected 3 layers, got %d", len(layers))
	}
	if len(layers[0]) != 1 || layers[0][0].ID != "a" {
		t.Errorf("layer 0 should be [a]")
	}
	if len(layers[1]) != 2 {
		t.Errorf("layer 1 should have 2 tasks, got %d", len(layers[1]))
	}
	if len(layers[2]) != 1 || layers[2][0].ID != "d" {
		t.Errorf("layer 2 should be [d]")
	}
}

func TestTopologicalSort_CycleDetection(t *testing.T) {
	tasks := []AgentTask{
		{ID: "a", AgentName: "agent1", Task: "task a", DependsOn: []string{"b"}},
		{ID: "b", AgentName: "agent2", Task: "task b", DependsOn: []string{"a"}},
	}
	_, err := TopologicalSort(tasks)
	if err == nil {
		t.Error("expected cycle detection error")
	}
}

func TestExecuteParallel_AllSucceed(t *testing.T) {
	var callCount atomic.Int32
	executor := func(ctx context.Context, task AgentTask) (*AgentResult, error) {
		callCount.Add(1)
		time.Sleep(10 * time.Millisecond)
		return &AgentResult{AgentName: task.AgentName, Output: "done: " + task.Task, Duration: 10 * time.Millisecond}, nil
	}
	orch := NewAgentOrchestrator(nil, 4)
	tasks := []AgentTask{
		{ID: "1", AgentName: "a1", Task: "t1"},
		{ID: "2", AgentName: "a2", Task: "t2"},
		{ID: "3", AgentName: "a3", Task: "t3"},
	}
	results, err := orch.ExecuteParallel(context.Background(), tasks, executor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if callCount.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", callCount.Load())
	}
	for i, r := range results {
		if r.Error != nil {
			t.Errorf("result %d has error: %v", i, r.Error)
		}
	}
}

func TestExecuteParallel_PartialFailure(t *testing.T) {
	executor := func(ctx context.Context, task AgentTask) (*AgentResult, error) {
		if task.AgentName == "a2" {
			return nil, fmt.Errorf("agent a2 failed")
		}
		return &AgentResult{AgentName: task.AgentName, Output: "ok"}, nil
	}
	orch := NewAgentOrchestrator(nil, 4)
	tasks := []AgentTask{
		{ID: "1", AgentName: "a1", Task: "t1"},
		{ID: "2", AgentName: "a2", Task: "t2"},
		{ID: "3", AgentName: "a3", Task: "t3"},
	}
	results, err := orch.ExecuteParallel(context.Background(), tasks, executor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results[1].Error == nil {
		t.Error("expected error for a2")
	}
	if results[0].Error != nil {
		t.Errorf("a1 should succeed: %v", results[0].Error)
	}
	if results[2].Error != nil {
		t.Errorf("a3 should succeed: %v", results[2].Error)
	}
}
