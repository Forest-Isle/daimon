package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

type Assertion = AssertionResult

// ContextChunk is an incremental context fragment from PERCEIVE to PLAN.
type ContextChunk struct {
	Source   string
	Content  string
	Priority int
	IsLast   bool
}

// PipelineChannels holds the communication channels between streaming phases.
type PipelineChannels struct {
	ContextChunks     chan *ContextChunk
	SubtaskChunks     chan *SubTask
	ObservationChunks chan *Observation
	AssertionChunks   chan *Assertion
	StreamText        chan string
	ErrorCh           chan error
	DoneCh            chan struct{}
}

func NewPipelineChannels() *PipelineChannels {
	return &PipelineChannels{
		ContextChunks:     make(chan *ContextChunk, 32),
		SubtaskChunks:     make(chan *SubTask, 16),
		ObservationChunks: make(chan *Observation, 32),
		AssertionChunks:   make(chan *Assertion, 32),
		StreamText:        make(chan string, 64),
		ErrorCh:           make(chan error, 4),
		DoneCh:            make(chan struct{}),
	}
}

// StreamingPipeline wraps streaming versions of all 5 phases.
type StreamingPipeline struct {
	perceiver *StreamingPerceiver
	planner   *StreamingPlanner
	executor  *StreamingExecutor
	observer  *StreamingObserver
	reflector *StreamingReflector
	channels  *PipelineChannels
}

func NewStreamingPipeline(
	perceiver *Perceiver,
	planner *Planner,
	executor *Executor,
	observer *Observer,
	reflector *Reflector,
) *StreamingPipeline {
	return &StreamingPipeline{
		perceiver: NewStreamingPerceiver(perceiver),
		planner:   NewStreamingPlanner(planner),
		executor:  NewStreamingExecutor(executor),
		observer:  NewStreamingObserver(observer),
		reflector: NewStreamingReflector(reflector),
		channels:  NewPipelineChannels(),
	}
}

// PipelineResult holds the complete result of a streaming pipeline run.
type PipelineResult struct {
	Plan            *TaskPlan
	ObsResult       *ObservationResult
	Reflection      *Reflection
	FinalAnswer     string
	ReplanRequested bool
}

// Run executes one complete pass through the streaming pipeline.
func (sp *StreamingPipeline) Run(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	state *CognitiveState,
	replanAttempt int,
) (*PipelineResult, error) {
	if sp.channels == nil {
		sp.channels = NewPipelineChannels()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer close(sp.channels.DoneCh)

	var (
		wg         sync.WaitGroup
		plan       *TaskPlan
		obsResult  *ObservationResult
		reflection *Reflection
		planErr    error
		observeErr error
	)

	planDone := make(chan struct{})
	observeDone := make(chan struct{})

	sendErr := func(err error) {
		if err == nil {
			return
		}
		select {
		case sp.channels.ErrorCh <- err:
		default:
		}
		cancel()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(sp.channels.ContextChunks)
		if err := sp.perceiver.Stream(ctx, state, sp.channels.ContextChunks); err != nil {
			sendErr(fmt.Errorf("perceive: %w", err))
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(planDone)
		defer close(sp.channels.SubtaskChunks)
		plan, planErr = sp.planner.Stream(ctx, state, sp.channels.ContextChunks, sp.channels.SubtaskChunks, sp.channels.StreamText)
		if planErr != nil {
			sendErr(fmt.Errorf("plan: %w", planErr))
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(sp.channels.ObservationChunks)
		if err := sp.executor.Stream(ctx, ch, sess, target, sp.channels.SubtaskChunks, sp.channels.ObservationChunks, sp.channels.StreamText); err != nil {
			sendErr(fmt.Errorf("act: %w", err))
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(observeDone)
		defer close(sp.channels.AssertionChunks)
		obsResult, observeErr = sp.observer.Stream(ctx, sp.channels.ObservationChunks, sp.channels.AssertionChunks)
		if observeErr != nil {
			sendErr(fmt.Errorf("observe: %w", observeErr))
		}
	}()

	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()

	select {
	case err := <-sp.channels.ErrorCh:
		<-waitDone
		close(sp.channels.StreamText)
		return nil, err
	case <-planDone:
	}

	select {
	case err := <-sp.channels.ErrorCh:
		<-waitDone
		close(sp.channels.StreamText)
		return nil, err
	case <-observeDone:
	}

	reflectErr := error(nil)
	reflection, reflectErr = sp.reflector.Stream(
		ctx,
		state,
		plan,
		obsResult,
		sp.channels.AssertionChunks,
		sp.channels.StreamText,
		replanAttempt,
	)
	if reflectErr != nil {
		cancel()
		<-waitDone
		close(sp.channels.StreamText)
		return nil, fmt.Errorf("reflect: %w", reflectErr)
	}

	<-waitDone
	close(sp.channels.StreamText)

	if reflection != nil && !reflection.Succeeded && obsResult != nil {
		passed := 0
		for _, a := range obsResult.Assertions {
			if a.Passed {
				passed++
			}
		}
		total := len(obsResult.Assertions)
		if total > 0 {
			assertRate := float64(passed) / float64(total)
			if assertRate >= 0.85 {
				slog.Info("reflect: overriding succeeded=false via high assertion pass rate",
					"assertion_rate", assertRate,
					"passed", passed,
					"total", total,
					"llm_confidence", reflection.OverallConfidence,
				)
				reflection.Succeeded = true
			}
		}
	}

	finalAnswer := ""
	if reflection != nil {
		finalAnswer = reflection.FinalAnswer
	}
	if finalAnswer == "" && plan != nil {
		finalAnswer = plan.DirectReply
	}

	return &PipelineResult{
		Plan:            plan,
		ObsResult:       obsResult,
		Reflection:      reflection,
		FinalAnswer:     finalAnswer,
		ReplanRequested: reflection != nil && reflection.NeedsReplan,
	}, nil
}
