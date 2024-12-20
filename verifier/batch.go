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
   regionModifications map[string]modificationInfo
   objectModifications map[string]modificationInfo
   eventQueue         map[string][]eventInfo
}

type modificationInfo struct {
   created bool
   teeUpdated bool
}

type eventInfo struct {
   timestamp    string
   functionCall string
}

func NewBatchVerifier(state state.Mutable) *BatchVerifier {
   return &BatchVerifier{
       verifier:            New(state),
       regionModifications: make(map[string]modificationInfo),
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
   bv.regionModifications = make(map[string]modificationInfo)
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
           }
           bv.objectModifications[a.ID] = modificationInfo{created: true}

       case *actions.SendEventAction:
           events := bv.eventQueue[a.IDTo]
           // Check for duplicate events with same timestamp
           timestamp := roughtime.Now()
           for _, event := range events {
               if event.timestamp == timestamp {
                   return ErrDuplicateAction
               }
           }
           events = append(events, eventInfo{
               timestamp:    timestamp,
               functionCall: a.FunctionCall,
           })
           bv.eventQueue[a.IDTo] = events
           
       case *actions.SetInputObjectAction:
           // Verify no conflicts with other actions
           if info, exists := bv.objectModifications[a.ID]; exists && !info.created {
               return ErrConflictingAction
           }
       }

      case *actions.CreateRegionAction:
           if info, exists := bv.regionModifications[a.RegionID]; exists {
               if info.created {
                   return ErrDuplicateAction
               }
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
       }
   }
   return nil
}

// Add verification methods
func (bv *BatchVerifier) verifyCreateRegionInBatch(ctx context.Context, action *actions.CreateRegionAction) error {
   // Verify no conflicts with other region actions
   if info, exists := bv.regionModifications[action.RegionID]; exists {
       if info.teeUpdated {
           return ErrConflictingAction
       }
   }
   return nil
}

func (bv *BatchVerifier) verifyUpdateRegionInBatch(ctx context.Context, action *actions.UpdateRegionAction) error {
   // Verify region isn't being created in this batch
   if info, exists := bv.regionModifications[action.RegionID]; exists {
       if info.created {
           return ErrConflictingAction
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

func (bv *BatchVerifier) verifyCreateInBatch(ctx context.Context, action *actions.CreateObjectAction) error {
   // Just verify the object hasn't already been created in this batch
   if info, exists := bv.objectModifications[action.ID]; exists && info.created {
       return ErrDuplicateAction
   }
   return nil
}

func (bv *BatchVerifier) verifyEventInBatch(ctx context.Context, action *actions.SendEventAction) error {
   // Verify target object exists and isn't being created in this batch
   if info, exists := bv.objectModifications[action.IDTo]; exists && info.created {
       return ErrConflictingAction
   }
   return nil
}

func (bv *BatchVerifier) verifySetInputInBatch(ctx context.Context, action *actions.SetInputObjectAction) error {
   // Verify target object either exists or is being created in this batch
   if info, exists := bv.objectModifications[action.ID]; !exists && !info.created {
       return ErrConflictingAction
   }
   return nil
}

func (bv *BatchVerifier) verifyBatchConstraints(ctx context.Context) error {
   // Verify event time ordering
   if err := bv.verifyEventOrdering(ctx); err != nil {
       return err
   }
   return nil
}

func (bv *BatchVerifier) verifyEventOrdering(ctx context.Context) error {
   // Verify events are properly ordered by timestamp
   for _, events := range bv.eventQueue {
       lastTimestamp := ""
       for _, event := range events {
           if event.timestamp <= lastTimestamp {
               return ErrInvalidEventOrder
           }
           lastTimestamp = event.timestamp
       }
   }
   return nil
}


