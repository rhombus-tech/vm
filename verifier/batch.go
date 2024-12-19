// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package verifier

import (
    "context"
    "errors"
    "fmt"

    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/state"

    "github.com/rhombus-tech/vm/actions"
)

var (
    ErrBatchLimit        = errors.New("batch size exceeds limit")
    ErrDuplicateAction   = errors.New("duplicate action in batch")
    ErrConflictingAction = errors.New("conflicting actions in batch")
)

const (
    MaxBatchSize = 256 // Maximum number of actions in a batch
)

// BatchVerifier handles verification of multiple actions
type BatchVerifier struct {
    verifier *StateVerifier
    
    // Track object modifications within batch
    objectModifications map[string]modificationInfo
    eventQueue         map[string][]eventInfo
}

type modificationInfo struct {
    created    bool
    deleted    bool
    codeChange bool
    storage    bool
}

type eventInfo struct {
    priority     uint64
    functionCall string
}

func NewBatchVerifier(state state.Mutable) *BatchVerifier {
    return &BatchVerifier{
        verifier:            New(state),
        objectModifications: make(map[string]modificationInfo),
        eventQueue:         make(map[string][]eventInfo),
    }
}

// VerifyBatch verifies a batch of actions
func (bv *BatchVerifier) VerifyBatch(ctx context.Context, actions []chain.Action) error {
    if len(actions) > MaxBatchSize {
        return ErrBatchLimit
    }

    // Reset tracking maps
    bv.objectModifications = make(map[string]modificationInfo)
    bv.eventQueue = make(map[string][]eventInfo)

    // First pass: collect all modifications and check for conflicts
    if err := bv.analyzeActions(ctx, actions); err != nil {
        return err
    }

    // Second pass: verify each action in context of the batch
    for _, action := range actions {
        if err := bv.verifyAction(ctx, action); err != nil {
            return err
        }
    }

    // Verify global batch constraints
    return bv.verifyBatchConstraints(ctx)
}

// analyzeActions collects information about all actions in the batch
func (bv *BatchVerifier) analyzeActions(ctx context.Context, actions []chain.Action) error {
    for _, action := range actions {
        switch a := action.(type) {
        case *actions.CreateObjectAction:
            if info, exists := bv.objectModifications[a.ID]; exists {
                if info.created {
                    return ErrDuplicateAction
                }
                if info.deleted {
                    return ErrConflictingAction
                }
            }
            bv.objectModifications[a.ID] = modificationInfo{created: true}

        case *actions.DeleteObjectAction:
            if info, exists := bv.objectModifications[a.ID]; exists {
                if info.deleted {
                    return ErrDuplicateAction
                }
                if info.created || info.codeChange || info.storage {
                    return ErrConflictingAction
                }
            }
            bv.objectModifications[a.ID] = modificationInfo{deleted: true}

        case *actions.ChangeObjectCodeAction:
            if info, exists := bv.objectModifications[a.ID]; exists {
                if info.codeChange {
                    return ErrDuplicateAction
                }
                if info.deleted {
                    return ErrConflictingAction
                }
            }
            bv.objectModifications[a.ID] = modificationInfo{codeChange: true}

        case *actions.SendEventAction:
            events := bv.eventQueue[a.IDTo]
            // Check for duplicate events with same priority
            for _, event := range events {
                if event.priority == a.Priority {
                    return ErrDuplicateAction
                }
            }
            events = append(events, eventInfo{
                priority:     a.Priority,
                functionCall: a.FunctionCall,
            })
            bv.eventQueue[a.IDTo] = events
        }
    }
    return nil
}

// verifyAction verifies an individual action within the batch context
func (bv *BatchVerifier) verifyAction(ctx context.Context, action chain.Action) error {
    // First verify the action individually
    if err := bv.verifier.VerifyStateTransition(ctx, action); err != nil {
        return err
    }

    // Then verify in batch context
    switch a := action.(type) {
    case *actions.CreateObjectAction:
        return bv.verifyCreateInBatch(ctx, a)
    case *actions.DeleteObjectAction:
        return bv.verifyDeleteInBatch(ctx, a)
    case *actions.ChangeObjectCodeAction:
        return bv.verifyCodeChangeInBatch(ctx, a)
    case *actions.SendEventAction:
        return bv.verifyEventInBatch(ctx, a)
    }

    return nil
}

// Batch-specific verification methods

func (bv *BatchVerifier) verifyCreateInBatch(ctx context.Context, action *actions.CreateObjectAction) error {
    // Additional checks for object creation in batch context
    if info, exists := bv.objectModifications[action.ID]; exists && info.deleted {
        return ErrConflictingAction
    }
    return nil
}

func (bv *BatchVerifier) verifyDeleteInBatch(ctx context.Context, action *actions.DeleteObjectAction) error {
    // Check if object is referenced by any events in the batch
    if events, exists := bv.eventQueue[action.ID]; exists && len(events) > 0 {
        return ErrConflictingAction
    }
    return nil
}

func (bv *BatchVerifier) verifyCodeChangeInBatch(ctx context.Context, action *actions.ChangeObjectCodeAction) error {
    // Verify no conflicts with other modifications
    if info, exists := bv.objectModifications[action.ID]; exists {
        if info.deleted || (info.created && info.codeChange) {
            return ErrConflictingAction
        }
    }
    return nil
}

func (bv *BatchVerifier) verifyEventInBatch(ctx context.Context, action *actions.SendEventAction) error {
    // Check if target object is being modified/deleted in this batch
    if info, exists := bv.objectModifications[action.IDTo]; exists {
        if info.deleted {
            return ErrConflictingAction
        }
    }
    return nil
}

// verifyBatchConstraints verifies global batch constraints
func (bv *BatchVerifier) verifyBatchConstraints(ctx context.Context) error {
    // Verify no circular dependencies in events
    if err := bv.verifyNoEventCycles(ctx); err != nil {
        return err
    }

    // Verify total batch resource usage
    if err := bv.verifyBatchResources(ctx); err != nil {
        return err
    }

    return nil
}

// Helper methods for batch constraints

func (bv *BatchVerifier) verifyNoEventCycles(ctx context.Context) error {
    visited := make(map[string]bool)
    path := make(map[string]bool)

    for objectID := range bv.eventQueue {
        if err := bv.dfsEventCycle(objectID, visited, path); err != nil {
            return err
        }
    }
    return nil
}

func (bv *BatchVerifier) dfsEventCycle(objectID string, visited, path map[string]bool) error {
    if path[objectID] {
        return ErrCircularDependency
    }
    if visited[objectID] {
        return nil
    }

    visited[objectID] = true
    path[objectID] = true

    for _, event := range bv.eventQueue[objectID] {
        // Check if this event's function call creates dependencies on other objects
        // This would require knowledge of function call dependencies
    }

    path[objectID] = false
    return nil
}

func (bv *BatchVerifier) verifyBatchResources(ctx context.Context) error {
    // Calculate and verify total resource usage across the batch
    // This could include:
    // - Total storage changes
    // - Total code size changes
    // - Total number of events
    // - etc.
    return nil
}
