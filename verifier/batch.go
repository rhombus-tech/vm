// verifier/batch.go
package verifier

import (
	"context"
	"errors"
	"fmt"
	"time"

	// If TEEAttestation is actually in "github.com/rhombus-tech/vm/core"
	// you must import "core" not "actions"
	"github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/tee/proto"

	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/state"

	"github.com/rhombus-tech/vm/coordination"
)

var (
    // Example errors
    ErrBatchLimit         = errors.New("batch size exceeds limit")
    ErrDuplicateAction    = errors.New("duplicate action in batch")
    ErrConflictingAction  = errors.New("conflicting actions in batch")
    ErrInvalidTimestamp   = errors.New("invalid timestamp ordering")
    ErrDuplicateAttestation = errors.New("duplicate attestation in batch")
    ErrCoordinationFailed = errors.New("regional coordination failed")

    MaxBatchSize = 256
)

type BatchVerifier struct {
    verifier    *StateVerifier
    coordinator *coordination.Coordinator

    // Tracking data for the batch:
    regionModifications map[string]modificationInfo
    objectModifications map[string]modificationInfo
    eventQueue          map[string][]eventInfo
    attestationsSeen    map[string]bool // track used attestation combos

    regionTasks         map[string]*coordination.Task
}

type modificationInfo struct {
    created     bool
    teeUpdated  bool
    coordinated bool
}

type eventInfo struct {
    timestamp    time.Time
    functionCall string
    // Since your TEEAttestation is actually in "core"
    attestations [2]core.TEEAttestation
    regionID     string
}

func NewBatchVerifier(st state.Mutable, coord *coordination.Coordinator) *BatchVerifier {
    return &BatchVerifier{
        verifier:            New(st),
        coordinator:         coord,
        regionModifications: make(map[string]modificationInfo),
        objectModifications: make(map[string]modificationInfo),
        eventQueue:          make(map[string][]eventInfo),
        attestationsSeen:    make(map[string]bool),
        regionTasks:         make(map[string]*coordination.Task),
    }
}

//------------------------------------------------

// Example top-level function that does the batch verification
func (bv *BatchVerifier) VerifyBatch(ctx context.Context, actions []chain.Action) error {
    if len(actions) > MaxBatchSize {
        return ErrBatchLimit
    }
    // Reset the maps for each new batch
    bv.regionModifications = make(map[string]modificationInfo)
    bv.objectModifications = make(map[string]modificationInfo)
    bv.eventQueue = make(map[string][]eventInfo)
    bv.attestationsSeen = make(map[string]bool)
    bv.regionTasks = make(map[string]*coordination.Task)

    // 1. Analyze all actions first
    if err := bv.analyzeActions(ctx, actions); err != nil {
        return err
    }
    // 2. Verify each action with context
    for _, action := range actions {
        if err := bv.verifyAction(ctx, action); err != nil {
            return err
        }
    }
    // 3. Check final constraints if needed
    return bv.verifyBatchConstraints(ctx)
}

//------------------------------------------------
// Step #1: gather or “analyze” actions
func (bv *BatchVerifier) analyzeActions(ctx context.Context, acts []chain.Action) error {
    for _, act := range acts {
        // Must do type-switch with pointer types
        switch a := act.(type) {
        case *actions.CreateObjectAction:
            // e.g. track that an object was created
            if info, exists := bv.objectModifications[a.ID]; exists && info.created {
                return ErrDuplicateAction
            }
            bv.objectModifications[a.ID] = modificationInfo{created: true}

        case *actions.SendEventAction:
            // Track event info
            evts := bv.eventQueue[a.IDTo]

            // Mark TEE attestation pairs as used:
            for _, att := range a.Attestations {
                attID := fmt.Sprintf("%x:%s", att.EnclaveID, att.Timestamp)
                if bv.attestationsSeen[attID] {
                    return ErrDuplicateAttestation
                }
                bv.attestationsSeen[attID] = true
            }
            // Suppose we want to ensure chronological ordering
            // For example, we just store the last timestamp to compare
            // (Here we do something simplified)
            evts = append(evts, eventInfo{
                // Convert your attestations[0].Timestamp (time.Time) if needed
                timestamp:    a.Attestations[0].Timestamp,
                functionCall: a.FunctionCall,
                attestations: a.Attestations,
                regionID:     a.RegionID,
            })
            bv.eventQueue[a.IDTo] = evts

        case *actions.SetInputObjectAction:
            // Suppose we do something minimal
            if info, exists := bv.objectModifications[a.ID]; exists && !info.created {
                return ErrConflictingAction
            }

        case *actions.CreateRegionAction:
            if info, exists := bv.regionModifications[a.RegionID]; exists && info.created {
                return ErrDuplicateAction
            }
            bv.regionModifications[a.RegionID] = modificationInfo{created: true}

        case *actions.UpdateRegionAction:
            if info, exists := bv.regionModifications[a.RegionID]; exists {
                if info.teeUpdated {
                    return ErrDuplicateAction
                }
                if info.created {
                    return ErrConflictingAction
                }
            }
            bv.regionModifications[a.RegionID] = modificationInfo{teeUpdated: true}

        default:
            // No special tracking needed
        }
    }
    return nil
}

//------------------------------------------------
// Step #2: verify each action individually in the batch context
func (bv *BatchVerifier) verifyAction(ctx context.Context, act chain.Action) error {
    // also do “normal” single-action verification from your StateVerifier
    if err := bv.verifier.VerifyStateTransition(ctx, act); err != nil {
        return err
    }

    // Then do additional batch-level checks:
    switch a := act.(type) {
    case *actions.CreateObjectAction:
        return bv.verifyCreateInBatch(ctx, a)
    case *actions.SendEventAction:
        return bv.verifyEventInBatch(ctx, a)
    case *actions.SetInputObjectAction:
        return bv.verifySetInputInBatch(ctx, a)
    case *actions.CreateRegionAction:
        return bv.verifyCreateRegionInBatch(ctx, a)
    case *actions.UpdateRegionAction:
        return bv.verifyUpdateRegionInBatch(ctx, a)
    }
    return nil
}

// Example stubs for each specialized check:
func (bv *BatchVerifier) verifyCreateInBatch(_ context.Context, a *actions.CreateObjectAction) error {
    // For example, if we already marked object created
    if info, ok := bv.objectModifications[a.ID]; ok && info.created {
        return ErrDuplicateAction
    }
    return nil
}

func (bv *BatchVerifier) verifyEventInBatch(_ context.Context, a *actions.SendEventAction) error {
    // If we want a “coordinator” message:
    // If your coordinator *actually* has a “SendMessage(...)” method, call it
    // If not, remove or adapt
    /*
    msg := &coordination.Message{
        FromWorker: "someID",
        ToWorker:   "someOtherID",
        Type:       coordination.MessageTypeAttestation, // or remove if not needed
        Data:       a.Parameters,
    }
    err := bv.coordinator.SendMessage(msg) // <— but your coordinator might not have it
    if err != nil {
        return fmt.Errorf("%w: %v", ErrCoordinationFailed, err)
    }
    */
    return nil
}

func (bv *BatchVerifier) verifySetInputInBatch(_ context.Context, a *actions.SetInputObjectAction) error {
    // e.g. do we check if the object is newly created in same batch?
    return nil
}

func (bv *BatchVerifier) verifyCreateRegionInBatch(_ context.Context, a *actions.CreateRegionAction) error {
    // example
    if info, ok := bv.regionModifications[a.RegionID]; ok && info.created {
        return ErrConflictingAction
    }
    return nil
}

func (bv *BatchVerifier) verifyUpdateRegionInBatch(_ context.Context, a *actions.UpdateRegionAction) error {
    // example
    return nil
}

//------------------------------------------------
// Step #3: final post-check constraints
func (bv *BatchVerifier) verifyBatchConstraints(ctx context.Context) error {
    // Example: check event ordering
    for _, evts := range bv.eventQueue {
        var prev time.Time
        for _, eInfo := range evts {
            if eInfo.timestamp.Before(prev) {
                return ErrInvalidTimestamp
            }
            prev = eInfo.timestamp
        }
    }

    // If you used “bv.coordinator.WaitForTask(...)”:
    // That method doesn’t exist by default, so either remove or define it
    /*
    for regionID, task := range bv.regionTasks {
        if err := bv.coordinator.WaitForTask(ctx, task.ID); err != nil {
            return fmt.Errorf("%w: region %s: %v", ErrCoordinationFailed, regionID, err)
        }
    }
    */

    return nil
}

func (bv *BatchVerifier) verifyComputeExecution(
    ctx context.Context,
    act *actions.SendEventAction,
    result *proto.ExecutionResult,
) error {
    // Convert protobuf attestations to core attestations
    attestations := [2]core.TEEAttestation{}
    if len(result.Attestations) != 2 {
        return fmt.Errorf("expected 2 attestations, got %d", len(result.Attestations))
    }
    
    for i, att := range result.Attestations[:2] {
        timestamp, err := time.Parse(time.RFC3339, att.Timestamp)
        if err != nil {
            return fmt.Errorf("invalid timestamp format: %w", err)
        }
        
        attestations[i] = core.TEEAttestation{
            EnclaveID:   att.EnclaveId,
            Measurement: att.Measurement,
            Timestamp:   timestamp,
            Data:        att.Data,
            Signature:   att.Signature,
            RegionProof: att.RegionProof,
        }
    }

    // Verify TEE attestations match
    if err := bv.verifier.VerifyAttestationPair(ctx, attestations, nil); err != nil {
        return err
    }

    // Add missing helper methods
    if err := bv.verifyTimestampsHelper(attestations); err != nil {
        return err
    }

    // Add missing helper method
    if err := bv.verifyRegionHelper(act.RegionID, attestations); err != nil {
        return err
    }

    return nil
}

// Add helper methods that were missing
func (bv *BatchVerifier) verifyTimestampsHelper(attestations [2]core.TEEAttestation) error {
    // Verify timestamps match between attestations
    if !attestations[0].Timestamp.Equal(attestations[1].Timestamp) {
        return fmt.Errorf("timestamp mismatch between attestations")
    }

    // Verify timestamps are within acceptable range
    now := time.Now()
    for _, att := range attestations {
        diff := now.Sub(att.Timestamp)
        if diff > 5*time.Minute || diff < -5*time.Minute {
            return fmt.Errorf("attestation timestamp outside acceptable range")
        }
    }
    
    return nil
}

func (bv *BatchVerifier) verifyRegionHelper(regionID string, attestations [2]core.TEEAttestation) error {
    // Verify the attestations are from TEEs in the specified region
    for _, att := range attestations {
        if len(att.RegionProof) == 0 {
            return fmt.Errorf("missing region proof in attestation")
        }
        // Here you would add actual region proof verification logic
        // This is just a placeholder - implement according to your region proof format
    }
    
    return nil
}