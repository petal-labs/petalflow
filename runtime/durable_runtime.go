package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
)

// runDurable executes the sequential checkpointed path. Parallel execution is
// deliberately rejected here because a single queue checkpoint cannot safely
// describe in-flight fan-out work.
func (r *BasicRuntime) runDurable(ctx context.Context, g graph.Graph, env *core.Envelope, opts RunOptions) (*core.Envelope, error) {
	if opts.Concurrency > 1 {
		return nil, errors.New("durable execution currently requires Concurrency <= 1")
	}
	if opts.MaxHops <= 0 {
		opts.MaxHops = 100
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := validateGraph(g); err != nil {
		return nil, err
	}

	runID := opts.RunID
	if runID == "" {
		runID = generateRunID()
	}
	record, loadErr := opts.RunStore.Get(ctx, runID)
	resuming := loadErr == nil
	if loadErr != nil && !errors.Is(loadErr, ErrRunNotFound) {
		return nil, fmt.Errorf("load run %s: %w", runID, loadErr)
	}
	if resuming {
		if env != nil && !opts.Resume {
			return nil, fmt.Errorf("run %s already exists; use Resume", runID)
		}
		if record.Checkpoint == nil || record.Checkpoint.Envelope == nil {
			return nil, fmt.Errorf("run %s has no recoverable checkpoint", runID)
		}
		if opts.IdempotencyKey == "" {
			opts.IdempotencyKey = record.IdempotencyKey
		}
		if opts.ResumeToken != "" && opts.ResumeToken != record.ResumeToken {
			return nil, fmt.Errorf("%w for run %s", ErrInvalidResumeToken, runID)
		}
		if record.WorkflowVersion != "" && opts.WorkflowVersion != "" && record.WorkflowVersion != opts.WorkflowVersion {
			return nil, fmt.Errorf("%w: run %s belongs to %q, got %q", ErrWorkflowVersion, runID, record.WorkflowVersion, opts.WorkflowVersion)
		}
		if record.MaxHops > 0 {
			opts.MaxHops = record.MaxHops
		}
		if record.ContinueOnError {
			opts.ContinueOnError = true
		}
		if record.NodeTimeout > 0 && opts.NodeTimeout <= 0 {
			opts.NodeTimeout = record.NodeTimeout
		}
		if isTerminal(record.Status) {
			// Failed runs and context-canceled runs may be retried from their
			// pre-node checkpoint. Explicit API cancellation and completed runs
			// are terminal and must not be replayed.
			recoverable := (record.Status == RunStatusFailed ||
				record.Status == RunStatusCanceled && !record.CancelRequested) &&
				record.Checkpoint != nil && len(record.Checkpoint.Queue) > 0
			if !recoverable {
				return nil, ErrRunAlreadySettled
			}
		}
		env = record.Checkpoint.Envelope.Clone()
		record.Status = RunStatusRunning
		record.Error = ""
		record.CompletedAt = time.Time{}
	} else {
		if env == nil {
			env = core.NewEnvelope()
		}
		if env.Vars == nil {
			env.Vars = make(map[string]any)
		}
		env.Trace.RunID = runID
		env.Trace.Started = opts.Now()
		record = &RunRecord{
			ID:              runID,
			GraphName:       g.Name(),
			WorkflowID:      opts.WorkflowID,
			WorkflowVersion: opts.WorkflowVersion,
			IdempotencyKey:  opts.IdempotencyKey,
			ResumeToken:     generateRunID(),
			Status:          RunStatusRunning,
			MaxHops:         opts.MaxHops,
			ContinueOnError: opts.ContinueOnError,
			NodeTimeout:     opts.NodeTimeout,
			StartedAt:       env.Trace.Started,
			UpdatedAt:       env.Trace.Started,
			Checkpoint:      newCheckpoint(runID, env, []string{g.Entry()}, nil, nil),
		}
		if err := opts.RunStore.Create(ctx, record); err != nil {
			return nil, fmt.Errorf("create run %s: %w", runID, err)
		}
	}

	if record.GraphName == "" {
		record.GraphName = g.Name()
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = opts.Now()
	}
	if env.Trace.RunID == "" {
		env.Trace.RunID = runID
	}

	seq := newSeqGenFrom(record.LastSeq)
	var emitMu sync.Mutex
	var persistenceErr error
	emit := func(event Event) {
		emitMu.Lock()
		defer emitMu.Unlock()
		event.Seq = seq.Next()
		record.LastSeq = event.Seq
		record.UpdatedAt = opts.Now()
		if opts.EventBus != nil {
			opts.EventBus.Publish(event)
		}
		if opts.EventHandler != nil {
			opts.EventHandler(event)
		}
		// Nodes may update the record through a human bridge while this event is
		// being emitted. Preserve those fields when recording the new sequence.
		if latest, err := opts.RunStore.Get(context.Background(), runID); err == nil {
			record.PendingAction = latest.PendingAction
			record.CancelRequested = latest.CancelRequested
		}
		if err := opts.RunStore.Update(context.Background(), record); err != nil && persistenceErr == nil {
			persistenceErr = err
		}
		select {
		case r.eventCh <- event:
		default:
			r.droppedEvents.Add(1)
		}
	}
	if opts.EventEmitterDecorator != nil {
		emit = opts.EventEmitterDecorator(emit)
	}

	if resuming {
		emit(NewEvent(EventRunResumed, runID).
			WithPayload("checkpoint_id", record.Checkpoint.ID).
			WithPayload("graph", g.Name()))
	} else {
		event := NewEvent(EventRunStarted, runID).
			WithPayload("graph", g.Name()).
			WithPayload("entry", g.Entry())
		if opts.TriggerSource != "" {
			event = event.WithPayload("trigger", opts.TriggerSource)
		}
		if opts.WorkflowID != "" {
			event = event.WithPayload("workflow_id", opts.WorkflowID)
		}
		emit(event)
	}

	checkpoint := record.Checkpoint
	if checkpoint == nil {
		checkpoint = newCheckpoint(runID, env, []string{g.Entry()}, nil, nil)
	}
	result, runErr := r.executeDurableSequential(ctx, g, env, opts, emit, record.StartedAt, checkpoint, record)
	// A human bridge may have written a pending action while the node was
	// executing. Refresh that field before the terminal lifecycle update so the
	// checkpoint writer cannot accidentally erase it.
	if latest, err := opts.RunStore.Get(context.Background(), runID); err == nil {
		record.PendingAction = latest.PendingAction
		record.CancelRequested = latest.CancelRequested
	}
	if persistenceErr != nil {
		runErr = fmt.Errorf("persist run %s: %w", runID, persistenceErr)
	}
	status := RunStatusCompleted
	if runErr != nil {
		status = RunStatus(runStatusForError(runErr))
		if status == RunStatusPaused {
			record.Error = ""
		} else {
			record.Error = runErr.Error()
		}
	}
	if cancelErr := r.cancellationError(ctx, opts.RunStore, runID); cancelErr != nil {
		status = RunStatusCanceled
		runErr = cancelErr
		record.Error = cancelErr.Error()
	}
	record.Status = status
	if isTerminal(status) {
		record.CompletedAt = opts.Now()
		record.UpdatedAt = record.CompletedAt
	} else {
		record.CompletedAt = time.Time{}
		record.UpdatedAt = opts.Now()
	}
	if status == RunStatusCompleted {
		record.Checkpoint = newCheckpoint(runID, result, nil, checkpoint.Visited, checkpoint.HopCount)
	} else {
		record.Checkpoint = checkpoint
	}
	finish := NewEvent(EventRunFinished, runID).
		WithElapsed(opts.Now().Sub(record.StartedAt)).
		WithPayload("status", string(status))
	if runErr != nil {
		finish = finish.WithPayload("error", runErr.Error())
	}
	emit(finish)
	if err := opts.RunStore.Update(context.Background(), record); err != nil && runErr == nil {
		runErr = fmt.Errorf("persist final run state: %w", err)
	}
	return result, runErr
}

func (r *BasicRuntime) cancellationError(ctx context.Context, store RunStore, runID string) error {
	if err := checkRunContext(ctx); err != nil {
		return err
	}
	record, err := store.Get(context.Background(), runID)
	if err == nil && record.CancelRequested {
		return fmt.Errorf("%w: cancellation requested", ErrRunCanceled)
	}
	return nil
}

func (r *BasicRuntime) executeDurableSequential(
	ctx context.Context,
	g graph.Graph,
	env *core.Envelope,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
	checkpoint *Checkpoint,
	record *RunRecord,
) (*core.Envelope, error) {
	queue := append([]string(nil), checkpoint.Queue...)
	visited := cloneBoolMap(checkpoint.Visited)
	if visited == nil {
		visited = make(map[string]bool)
	}
	hops := cloneIntMap(checkpoint.HopCount)
	if hops == nil {
		hops = make(map[string]int)
	}
	current := env
	for len(queue) > 0 {
		if err := checkRunContext(ctx); err != nil {
			return current, err
		}
		nodeID := queue[0]
		queue = queue[1:]
		if visited[nodeID] && hops[nodeID] >= opts.MaxHops {
			continue
		}
		hops[nodeID]++
		if hops[nodeID] > opts.MaxHops {
			return current, fmt.Errorf("%w: node %s executed %d times", ErrMaxHopsExceeded, nodeID, hops[nodeID])
		}
		node, ok := g.NodeByID(nodeID)
		if !ok {
			return current, fmt.Errorf("%w: %s", graph.ErrNodeNotFound, nodeID)
		}

		checkpoint.Queue = append([]string{nodeID}, queue...)
		checkpoint.Visited = cloneBoolMap(visited)
		checkpoint.HopCount = cloneIntMap(hops)
		checkpoint.NodeStatuses = cloneNodeStatusMap(checkpoint.NodeStatuses)
		if checkpoint.NodeStatuses == nil {
			checkpoint.NodeStatuses = make(map[string]NodeStatus)
		}
		checkpoint.NodeStatuses[nodeID] = NodeStatusRunning
		checkpoint.Envelope = current.Clone()
		checkpoint.ID = generateRunID()
		checkpoint.CreatedAt = opts.Now()
		record.Checkpoint = checkpoint
		record.Status = RunStatusRunning
		record.UpdatedAt = opts.Now()
		if err := opts.RunStore.Update(context.Background(), record); err != nil {
			return current, fmt.Errorf("persist checkpoint: %w", err)
		}
		emit(NewEvent(EventRunSnapshot, current.Trace.RunID).
			WithNode(node.ID(), node.Kind()).
			WithPayload("checkpoint_id", checkpoint.ID).
			WithPayload("next_node", node.ID()))

		var skip bool
		var err error
		current, skip, err = r.handleSequentialBeforeStep(ctx, g, node, current, opts, emit, runStart, hops[nodeID])
		if err != nil {
			return current, err
		}
		if !skip {
			result, nodeErr := r.executeNode(ctx, node, current, opts, emit, runStart)
			if nodeErr != nil {
				checkpoint.NodeStatuses[nodeID] = NodeStatusFailed
				if pendingNodeError(nodeErr) {
					checkpoint.NodeStatuses[nodeID] = NodeStatusPending
				}
				record.Checkpoint = checkpoint
				record.UpdatedAt = opts.Now()
				if err := opts.RunStore.Update(context.Background(), record); err != nil {
					return current, fmt.Errorf("persist node status: %w", err)
				}
			}
			// A cancellation that arrives while a node is running must leave the
			// pre-node checkpoint intact. Its result is never committed.
			if err := checkRunContext(ctx); err != nil {
				return current, err
			}
			if err := r.handleSequentialAfterStep(ctx, g, node, current, result, nodeErr, opts, emit, runStart, hops[nodeID]); err != nil {
				return current, err
			}
			if pendingNodeError(nodeErr) {
				return current, ErrHumanPending
			}
			current, err = resolveSequentialNodeOutcome(current, result, node, nodeErr, hops[nodeID], opts, nodeID)
			if err != nil {
				return current, err
			}
		}
		if skip {
			checkpoint.NodeStatuses[nodeID] = NodeStatusSkipped
		} else {
			checkpoint.NodeStatuses[nodeID] = NodeStatusCompleted
		}
		visited[nodeID] = true
		next := r.determineSuccessors(g, node, current, emit, runStart, opts)
		queue = append(queue, next...)
		checkpoint.Queue = append([]string(nil), queue...)
		checkpoint.Visited = cloneBoolMap(visited)
		checkpoint.HopCount = cloneIntMap(hops)
		checkpoint.Envelope = current.Clone()
		checkpoint.ID = generateRunID()
		checkpoint.CreatedAt = opts.Now()
		record.Checkpoint = checkpoint
		record.Status = RunStatusRunning
		record.UpdatedAt = opts.Now()
		if err := opts.RunStore.Update(context.Background(), record); err != nil {
			return current, fmt.Errorf("persist checkpoint: %w", err)
		}
		emit(NewEvent(EventRunSnapshot, current.Trace.RunID).
			WithNode(node.ID(), node.Kind()).
			WithPayload("checkpoint_id", checkpoint.ID).
			WithPayload("next_nodes", append([]string(nil), queue...)))
	}
	return current, nil
}

func pendingNodeError(err error) bool {
	if err == nil {
		return false
	}
	var pending PendingError
	return errors.As(err, &pending) && pending.Pending()
}

func newCheckpoint(runID string, env *core.Envelope, queue []string, visited map[string]bool, hops map[string]int) *Checkpoint {
	return &Checkpoint{
		ID:        generateRunID(),
		RunID:     runID,
		CreatedAt: time.Now().UTC(),
		Envelope:  env.Clone(),
		Queue:     append([]string(nil), queue...),
		Visited:   cloneBoolMap(visited),
		HopCount:  cloneIntMap(hops),
	}
}
