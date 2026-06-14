package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/taskruntime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandSubsystemRegistersUserVisibleCommands(t *testing.T) {
	cfg := testConfig(t)
	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	for _, cmd := range []string{"/feature", "/memory", "/skills", "/skill", "/sleep", "/tasks", "/resume", "/team", "/reset"} {
		if _, ok := gw.commands.Table[cmd]; !ok {
			t.Fatalf("command %s is not registered", cmd)
		}
	}
}

func TestFeatureCommandPersistsRuntimeOverride(t *testing.T) {
	cfg := testConfig(t)
	cfg.Memory.Enabled = true
	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	msg := channel.InboundMessage{Channel: "tui", ChannelID: "test", Text: "/feature disable memory"}
	resp, handled := gw.commands.Dispatch(context.Background(), &nullChannel{}, msg)
	require.True(t, handled)
	assert.Contains(t, resp, "Feature memory disabled")
	assert.False(t, gw.features.IsEnabled("memory"))

	statePath := filepath.Join(appdir.BaseDir(), "feature_state.json")
	data, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(data), `"memory": false`), string(data))
}

func TestTaskCheckpointFeedsResumeCommand(t *testing.T) {
	cfg := testConfig(t)
	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	ctx := context.Background()
	msg := channel.InboundMessage{Channel: "tui", ChannelID: "task-checkpoint", Text: "continue"}
	sess, err := gw.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	require.NoError(t, err)
	sess.SetMetadata("plan", `{"goal":"resume checkpoint"}`)
	sess.AddMessage(session.Message{ID: "m1", Role: "tool_result", Content: "tests passed", CreatedAt: time.Now()})
	sess.AddMessage(session.Message{ID: "m2", Role: "assistant", Content: "final answer", CreatedAt: time.Now()})

	result := gw.saveTaskCheckpoint(ctx, msg)
	require.Equal(t, "final answer", result)

	resp, err := gw.handleResume(ctx, &nullChannel{}, channel.InboundMessage{
		Channel:   "tui",
		ChannelID: "task-checkpoint",
		Text:      "/resume " + sess.ID,
	})
	require.NoError(t, err)
	assert.Contains(t, resp, "resume checkpoint")
	assert.Contains(t, resp, "tests passed")
	assert.Contains(t, resp, "final answer")
}

func TestTasksCommandShowsLedgerInspectionFields(t *testing.T) {
	cfg := testConfig(t)
	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	_, err = gw.taskLedger.Create(context.Background(), taskruntime.CreateInput{
		Kind:        "user_request",
		Title:       "Inspect long task",
		Description: "Long-running task",
		Assignee:    "orchestrator",
		Metadata: taskruntime.Metadata{
			Goal:       "inspect long task",
			NextAction: "resume from checkpoint",
			Evidence:   []string{"checkpoint saved"},
		},
	})
	require.NoError(t, err)

	resp, handled := gw.commands.Dispatch(context.Background(), &nullChannel{}, channel.InboundMessage{Text: "/tasks"})
	require.True(t, handled)
	assert.Contains(t, resp, "inspect long task")
	assert.Contains(t, resp, "resume from checkpoint")
	assert.Contains(t, resp, "evidence: 1 item")
}

func TestTeamBackgroundCommands(t *testing.T) {
	cfg := testConfig(t)
	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	gw.multiAgent.BgManager = agent.NewBackgroundManager()
	agentID := gw.multiAgent.BgManager.Spawn(context.Background(),
		&agent.AgentSpec{Name: "worker", Description: "does work"},
		func(ctx context.Context) (*agent.AgentResult, error) {
			return &agent.AgentResult{AgentName: "worker", Output: "background done"}, nil
		})
	_, err = gw.multiAgent.BgManager.Wait(context.Background(), agentID)
	require.NoError(t, err)

	status, handled := gw.commands.Dispatch(context.Background(), &nullChannel{}, channel.InboundMessage{Text: "/team status"})
	require.True(t, handled)
	assert.Contains(t, status, "agent_worker")

	result, handled := gw.commands.Dispatch(context.Background(), &nullChannel{}, channel.InboundMessage{Text: "/team attach " + agentID})
	require.True(t, handled)
	assert.Contains(t, result, "background done")

	cancelResp, handled := gw.commands.Dispatch(context.Background(), &nullChannel{}, channel.InboundMessage{Text: "/team cancel missing"})
	require.True(t, handled)
	assert.Contains(t, cancelResp, "unknown background agent")
}
