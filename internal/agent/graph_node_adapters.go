package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

// NodeDeps groups phase dependencies for graph-backed execution.
type NodeDeps struct {
	Perceiver *Perceiver
	Planner   *Planner
	Executor  *Executor
	Observer  *Observer
	Reflector *Reflector
	Sessions  *session.Manager
	Channel   channel.Channel
}

type graphNodePayload struct {
	CogState     *CognitiveState    `json:"cog_state,omitempty"`
	TaskPlan     *TaskPlan          `json:"task_plan,omitempty"`
	Observations []Observation      `json:"observations,omitempty"`
	ObsResult    *ObservationResult `json:"obs_result,omitempty"`
	Reflection   *Reflection        `json:"reflection,omitempty"`
}

func cogStateFromGraphState(state GraphState) (*CognitiveState, error) {
	for i := len(state.Events) - 1; i >= 0; i-- {
		snapshot := strings.TrimSpace(state.Events[i].OutputSnapshot)
		if snapshot == "" {
			continue
		}

		var payload graphNodePayload
		if err := json.Unmarshal([]byte(snapshot), &payload); err == nil && payload.CogState != nil {
			return payload.CogState, nil
		}

		var cogState CognitiveState
		if err := json.Unmarshal([]byte(snapshot), &cogState); err == nil {
			return &cogState, nil
		}

		return nil, fmt.Errorf("decode cognitive state from snapshot: invalid payload")
	}

	return &CognitiveState{}, nil
}

func cogStateToSnapshot(cs *CognitiveState) string {
	if cs == nil {
		return "{}"
	}

	raw, err := json.Marshal(cs)
	if err != nil {
		return "{}"
	}

	return string(raw)
}

func payloadFromGraphState(state GraphState) (*graphNodePayload, error) {
	for i := len(state.Events) - 1; i >= 0; i-- {
		snapshot := strings.TrimSpace(state.Events[i].OutputSnapshot)
		if snapshot == "" {
			continue
		}

		var payload graphNodePayload
		if err := json.Unmarshal([]byte(snapshot), &payload); err == nil {
			if payload.CogState != nil || payload.TaskPlan != nil || payload.ObsResult != nil || payload.Reflection != nil || payload.Observations != nil {
				return &payload, nil
			}
		}

		var cogState CognitiveState
		if err := json.Unmarshal([]byte(snapshot), &cogState); err == nil {
			return &graphNodePayload{CogState: &cogState}, nil
		}

		return nil, fmt.Errorf("decode graph payload from snapshot: invalid payload")
	}

	return &graphNodePayload{CogState: &CognitiveState{}}, nil
}

func payloadToSnapshot(payload *graphNodePayload) string {
	if payload == nil {
		return "{}"
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}

	return string(raw)
}

func loadSessionByID(ctx context.Context, mgr *session.Manager, sessionID string) (*session.Session, error) {
	if mgr == nil {
		return nil, fmt.Errorf("session manager is nil")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session ID is empty")
	}

	chain, err := mgr.GetSessionChain(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(chain) == 0 || chain[0] == nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	return chain[0], nil
}

func targetFromSession(sess *session.Session) channel.MessageTarget {
	if sess == nil {
		return channel.MessageTarget{}
	}

	return channel.MessageTarget{
		Channel:   sess.Channel,
		ChannelID: sess.ChannelID,
	}
}

func NewPerceiveNodeWithDeps(deps NodeDeps, userMsg, userID string) *FuncNode {
	return NewFuncNode(NodePerceive, func(ctx context.Context, state GraphState) (NodeResult, error) {
		if deps.Perceiver == nil {
			return NodeResult{}, fmt.Errorf("perceiver is nil")
		}
		if deps.Sessions == nil {
			return NodeResult{}, fmt.Errorf("session manager is nil")
		}

		sess, err := loadSessionByID(ctx, deps.Sessions, state.SessionID)
		if err != nil {
			return NodeResult{}, err
		}

		cogState, err := deps.Perceiver.Run(ctx, sess, userMsg, userID)
		if err != nil {
			return NodeResult{}, err
		}

		return NodeResult{
			NextNode: NodePlan,
			Output:   cogStateToSnapshot(cogState),
		}, nil
	})
}

func NewPlanNodeWithDeps(deps NodeDeps) *FuncNode {
	return NewFuncNode(NodePlan, func(ctx context.Context, state GraphState) (NodeResult, error) {
		if deps.Planner == nil {
			return NodeResult{}, fmt.Errorf("planner is nil")
		}

		cogState, err := cogStateFromGraphState(state)
		if err != nil {
			return NodeResult{}, err
		}

		taskPlan, err := deps.Planner.Run(ctx, cogState)
		if err != nil {
			return NodeResult{}, err
		}

		return NodeResult{
			NextNode: NodeAct,
			Output: payloadToSnapshot(&graphNodePayload{
				CogState: cogState,
				TaskPlan: taskPlan,
			}),
		}, nil
	})
}

func NewActNodeWithDeps(deps NodeDeps) *FuncNode {
	return NewFuncNode(NodeAct, func(ctx context.Context, state GraphState) (NodeResult, error) {
		if deps.Executor == nil {
			return NodeResult{}, fmt.Errorf("executor is nil")
		}
		if deps.Sessions == nil {
			return NodeResult{}, fmt.Errorf("session manager is nil")
		}

		payload, err := payloadFromGraphState(state)
		if err != nil {
			return NodeResult{}, err
		}
		if payload.CogState == nil {
			payload.CogState = &CognitiveState{}
		}
		if payload.TaskPlan == nil {
			return NodeResult{}, fmt.Errorf("task plan is missing from graph state")
		}

		sess, err := loadSessionByID(ctx, deps.Sessions, state.SessionID)
		if err != nil {
			return NodeResult{}, err
		}

		target := targetFromSession(sess)
		taskCtx := NewTaskContext(sess.ID, payload.CogState.Goal.Raw)
		observations, err := deps.Executor.RunWithContext(ctx, deps.Channel, sess, target, payload.TaskPlan, taskCtx)
		if err != nil {
			return NodeResult{}, err
		}

		return NodeResult{
			NextNode: NodeObserve,
			Output: payloadToSnapshot(&graphNodePayload{
				CogState:     payload.CogState,
				TaskPlan:     payload.TaskPlan,
				Observations: observations,
			}),
		}, nil
	})
}

func NewObserveNodeWithDeps(deps NodeDeps) *FuncNode {
	return NewFuncNode(NodeObserve, func(ctx context.Context, state GraphState) (NodeResult, error) {
		if deps.Observer == nil {
			return NodeResult{}, fmt.Errorf("observer is nil")
		}

		payload, err := payloadFromGraphState(state)
		if err != nil {
			return NodeResult{}, err
		}
		if payload.TaskPlan == nil {
			return NodeResult{}, fmt.Errorf("task plan is missing from graph state")
		}

		obsResult := deps.Observer.Run(payload.Observations, payload.TaskPlan)
		hasFailures := len(obsResult.Failures) > 0

		result := NodeResult{
			Output: payloadToSnapshot(&graphNodePayload{
				CogState:     payload.CogState,
				TaskPlan:     payload.TaskPlan,
				Observations: payload.Observations,
				ObsResult:    obsResult,
			}),
		}
		if hasFailures {
			result.NextNode = NodeReflect
		} else {
			result.ShouldTerminate = true
		}

		return result, nil
	})
}

func NewReflectNodeWithDeps(deps NodeDeps) *FuncNode {
	return NewFuncNode(NodeReflect, func(ctx context.Context, state GraphState) (NodeResult, error) {
		if deps.Reflector == nil {
			return NodeResult{}, fmt.Errorf("reflector is nil")
		}
		if deps.Sessions == nil {
			return NodeResult{}, fmt.Errorf("session manager is nil")
		}

		payload, err := payloadFromGraphState(state)
		if err != nil {
			return NodeResult{}, err
		}
		if payload.CogState == nil {
			payload.CogState = &CognitiveState{}
		}
		if payload.TaskPlan == nil {
			return NodeResult{}, fmt.Errorf("task plan is missing from graph state")
		}
		if payload.ObsResult == nil {
			return NodeResult{}, fmt.Errorf("observation result is missing from graph state")
		}

		sess, err := loadSessionByID(ctx, deps.Sessions, state.SessionID)
		if err != nil {
			return NodeResult{}, err
		}

		target := targetFromSession(sess)
		reflection, err := deps.Reflector.Run(ctx, deps.Channel, target, payload.CogState, payload.TaskPlan, payload.ObsResult, 0)
		if err != nil {
			return NodeResult{}, err
		}

		result := NodeResult{
			Output: payloadToSnapshot(&graphNodePayload{
				CogState:     payload.CogState,
				TaskPlan:     payload.TaskPlan,
				Observations: payload.Observations,
				ObsResult:    payload.ObsResult,
				Reflection:   reflection,
			}),
		}
		if reflection.NeedsReplan {
			result.NextNode = NodeReplan
		} else {
			result.ShouldTerminate = true
		}

		return result, nil
	})
}

func NewReplanNodeWithDeps(deps NodeDeps) *FuncNode {
	return NewFuncNode(NodeReplan, func(ctx context.Context, state GraphState) (NodeResult, error) {
		payload, err := payloadFromGraphState(state)
		if err != nil {
			return NodeResult{}, err
		}
		if payload.CogState == nil {
			payload.CogState = &CognitiveState{}
		}
		if payload.Reflection == nil {
			return NodeResult{}, fmt.Errorf("reflection is missing from graph state")
		}

		adjustment := strings.TrimSpace(payload.Reflection.SuggestedAdjustment)
		if adjustment != "" {
			if strings.TrimSpace(payload.CogState.UserMessage) == "" {
				payload.CogState.UserMessage = adjustment
			} else {
				payload.CogState.UserMessage = adjustment + "\n\n" + payload.CogState.UserMessage
			}
		}

		return NodeResult{
			NextNode: NodePlan,
			Output:   payloadToSnapshot(payload),
		}, nil
	})
}

func BuildGraphWithDeps(deps NodeDeps, userMsg, userID string) *ExecutionGraph {
	graph := &ExecutionGraph{}

	graph.Register(NewPerceiveNodeWithDeps(deps, userMsg, userID))
	graph.Register(NewPlanNodeWithDeps(deps))
	graph.Register(NewActNodeWithDeps(deps))
	graph.Register(NewObserveNodeWithDeps(deps))
	graph.Register(NewReflectNodeWithDeps(deps))
	graph.Register(NewReplanNodeWithDeps(deps))

	graph.AddEdge(NodePerceive, func(result NodeResult) NodeType {
		return NodePlan
	})
	graph.AddEdge(NodePlan, func(result NodeResult) NodeType {
		return NodeAct
	})
	graph.AddEdge(NodeAct, func(result NodeResult) NodeType {
		if !result.ShouldTerminate {
			return NodeObserve
		}
		return NodeTerminate
	})
	graph.AddEdge(NodeObserve, func(result NodeResult) NodeType {
		return result.NextNode
	})
	graph.AddEdge(NodeReflect, func(result NodeResult) NodeType {
		return result.NextNode
	})
	graph.AddEdge(NodeReplan, func(result NodeResult) NodeType {
		return NodePlan
	})

	return graph
}
