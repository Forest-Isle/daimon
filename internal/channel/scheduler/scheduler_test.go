package scheduler

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/taskruntime"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// mockNotifier implements channel.Channel for test assertions.
type mockNotifier struct {
	mu       sync.Mutex
	messages []channel.OutboundMessage
}

func (m *mockNotifier) Name() string                                            { return "mock" }
func (m *mockNotifier) Start(_ context.Context, _ channel.InboundHandler) error { return nil }
func (m *mockNotifier) Stop(_ context.Context) error                            { return nil }
func (m *mockNotifier) Send(_ context.Context, msg channel.OutboundMessage) error {
	m.mu.Lock()
	m.messages = append(m.messages, msg)
	m.mu.Unlock()
	return nil
}
func (m *mockNotifier) SendStreaming(_ context.Context, _ channel.MessageTarget) (channel.StreamUpdater, error) {
	return nil, nil
}
func (m *mockNotifier) lastMsg() *channel.OutboundMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		return nil
	}
	return &m.messages[len(m.messages)-1]
}

func TestNew(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	if sc.Name() != "scheduler" {
		t.Errorf("expected name 'scheduler', got %q", sc.Name())
	}
}

func TestAddAndListTasks(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	ctx := context.Background()
	task, err := sc.AddTask(ctx, "check email", "@every 1h", "telegram", "12345")
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if task.ID == "" {
		t.Error("expected non-empty ID")
	}
	if task.CronExpr != "@every 1h" {
		t.Errorf("expected '@every 1h', got %q", task.CronExpr)
	}

	tasks, err := sc.ListTasks(ctx)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Prompt != "check email" {
		t.Errorf("expected 'check email', got %q", tasks[0].Prompt)
	}
}

func TestRemoveTask(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	ctx := context.Background()
	task, _ := sc.AddTask(ctx, "test", "@daily", "telegram", "123")
	if err := sc.RemoveTask(ctx, task.ID); err != nil {
		t.Fatalf("RemoveTask: %v", err)
	}

	tasks, _ := sc.ListTasks(ctx)
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks after remove, got %d", len(tasks))
	}
}

func TestEnableDisable(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	ctx := context.Background()
	task, _ := sc.AddTask(ctx, "test", "@daily", "telegram", "123")

	if err := sc.SetEnabled(ctx, task.ID, false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}

	tasks, _ := sc.ListTasks(ctx)
	if tasks[0].Enabled {
		t.Error("expected task disabled")
	}

	if err := sc.SetEnabled(ctx, task.ID, true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}

	tasks, _ = sc.ListTasks(ctx)
	if !tasks[0].Enabled {
		t.Error("expected task enabled")
	}
}

func TestCronFiresTask(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var capturedMsg channel.InboundMessage
	var msgMu sync.Mutex
	handler := func(_ context.Context, msg channel.InboundMessage) {
		msgMu.Lock()
		capturedMsg = msg
		msgMu.Unlock()
	}

	task, err := sc.AddTask(ctx, "test prompt", "@every 1s", "telegram", "chat_42")
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	if err := sc.Start(ctx, handler); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(1500 * time.Millisecond)

	msgMu.Lock()
	got := capturedMsg
	msgMu.Unlock()

	if got.Channel != "scheduler" {
		t.Errorf("expected Channel 'scheduler', got %q", got.Channel)
	}
	if got.ChannelID != task.ID {
		t.Errorf("expected ChannelID %q, got %q", task.ID, got.ChannelID)
	}
	if got.Text != "test prompt" {
		t.Errorf("expected Text 'test prompt', got %q", got.Text)
	}

	reply := channel.OutboundMessage{Channel: "scheduler", ChannelID: task.ID, Text: "done"}
	if err := sc.Send(ctx, reply); err != nil {
		t.Fatalf("Send: %v", err)
	}

	last := notifier.lastMsg()
	if last == nil {
		t.Fatal("expected notifier to receive forwarded message")
	}
	if last.Channel != "telegram" {
		t.Errorf("expected Channel 'telegram', got %q", last.Channel)
	}
	if last.ChannelID != "chat_42" {
		t.Errorf("expected ChannelID 'chat_42', got %q", last.ChannelID)
	}
	if last.Text != "done" {
		t.Errorf("expected Text 'done', got %q", last.Text)
	}

	sc.Stop(context.Background())
}

func TestStartNoTasks(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	ctx := context.Background()
	if err := sc.Start(ctx, func(_ context.Context, _ channel.InboundMessage) {}); err != nil {
		t.Fatalf("Start with no tasks: %v", err)
	}
	sc.Stop(context.Background())
}

func TestRunOnce(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	ctx := context.Background()
	task, _ := sc.AddTask(ctx, "run once test", "@daily", "telegram", "999")

	var capturedMsg channel.InboundMessage
	var msgMu sync.Mutex
	_ = sc.Start(ctx, func(_ context.Context, msg channel.InboundMessage) {
		msgMu.Lock()
		capturedMsg = msg
		msgMu.Unlock()
	})
	defer sc.Stop(context.Background())

	if err := sc.RunOnce(ctx, task.ID); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	msgMu.Lock()
	got := capturedMsg
	msgMu.Unlock()

	if got.Text != "run once test" {
		t.Errorf("expected Text 'run once test', got %q", got.Text)
	}
}

func TestSchedulerRecordsTaskLedgerLifecycle(t *testing.T) {
	db := openTestDB(t)
	ledger := taskruntime.NewLedger(db.DB)
	notifier := &mockNotifier{}
	sc := New(db, notifier, ledger)

	ctx := context.Background()
	task, err := sc.AddTask(ctx, "ledger lifecycle", "@daily", "telegram", "chat_1")
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	entry, err := ledger.Get(ctx, taskruntime.ScheduledLedgerID(task.ID))
	if err != nil {
		t.Fatalf("ledger.Get after add: %v", err)
	}
	if entry.Kind != "scheduled" || entry.State != taskruntime.StatePending {
		t.Fatalf("entry after add = %#v", entry)
	}

	sc.fireTask(*task)
	entry, err = ledger.Get(ctx, taskruntime.ScheduledLedgerID(task.ID))
	if err != nil {
		t.Fatalf("ledger.Get after fire: %v", err)
	}
	if entry.State != taskruntime.StateRunning {
		t.Fatalf("state after fire = %s, want running", entry.State)
	}
	if entry.Metadata.ScheduledTaskID != task.ID || entry.Metadata.SessionChannelID != task.ID {
		t.Fatalf("metadata after fire = %#v", entry.Metadata)
	}

	sc.FinishRun(ctx, task.ID, nil, "done")
	entry, err = ledger.Get(ctx, taskruntime.ScheduledLedgerID(task.ID))
	if err != nil {
		t.Fatalf("ledger.Get after finish: %v", err)
	}
	if entry.State != taskruntime.StateSucceeded || entry.Result != "done" {
		t.Fatalf("entry after finish = %#v", entry)
	}

	var status string
	if err := db.QueryRowContext(ctx, `SELECT last_status FROM scheduled_tasks WHERE id = ?`, task.ID).Scan(&status); err != nil {
		t.Fatalf("last_status query: %v", err)
	}
	if status != "succeeded" {
		t.Fatalf("last_status = %q, want succeeded", status)
	}
}

func TestSetNotifier(t *testing.T) {
	db := openTestDB(t)
	sc := New(db, nil) // nil notifier initially

	if sc.notifier != nil {
		t.Error("expected nil notifier initially")
	}

	mock := &mockNotifier{}
	sc.SetNotifier(mock)

	if sc.notifier == nil {
		t.Error("expected non-nil notifier after SetNotifier")
	}
}

func TestCompileTimeChannelInterface(t *testing.T) {
	var _ channel.Channel = (*SchedulerChannel)(nil)
}
