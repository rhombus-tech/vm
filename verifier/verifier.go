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
   "github.com/rhombus-tech/vm/consts"
   "github.com/rhombus-tech/vm/storage"
)

var (
   ErrInputObjectMissing = errors.New("input object not found")
   ErrInvalidEventOrder  = errors.New("invalid event order")
   ErrInvalidAttestation = errors.New("invalid TEE attestation")
   ErrTimestampOutOfRange = errors.New("timestamp outside valid window")
)

type StateVerifier struct {
   state state.Mutable
}

func New(state state.Mutable) *StateVerifier {
   return &StateVerifier{
       state: state,
   }
}

func (v *StateVerifier) VerifySystemState(ctx context.Context) error {
   // Verify input object exists and is valid
   inputID, err := storage.GetInputObject(ctx, v.state)
   if err != nil {
       return err
   }
   if inputID == "" {
       return ErrInputObjectMissing
   }

   inputObject, err := storage.GetObject(ctx, v.state, inputID)
   if err != nil {
       return err
   }
   if inputObject == nil {
       return ErrInputObjectMissing
   }

   return nil
}

func (v *StateVerifier) VerifyObjectState(ctx context.Context, obj map[string][]byte) error {
   if code, exists := obj["code"]; exists {
       if len(code) > consts.MaxCodeSize {
           return actions.ErrCodeTooLarge
       }
   }

   if storage, exists := obj["storage"]; exists {
       if len(storage) > consts.MaxStorageSize {
           return actions.ErrStorageTooLarge
       }
   }

   return nil
}

func (v *StateVerifier) verifyAttestation(ctx context.Context, attestation actions.TEEAttestation, region map[string]interface{}) error {
   // Verify TEE is authorized for region
   tees := region["tees"].([]actions.TEEAddress)
   found := false
   for _, tee := range tees {
       if bytes.Equal(tee, attestation.EnclaveID) {
           found = true
           break
       }
   }
   if !found {
       return ErrInvalidAttestation
   }

   // Verify timestamp is within valid window
   currentTime := roughtime.Now()
   if !isTimeInWindow(attestation.Timestamp, currentTime) {
       return ErrTimestampOutOfRange
   }

   return nil
}

func (v *StateVerifier) verifyAttestationPair(ctx context.Context, attestations [2]actions.TEEAttestation, region map[string]interface{}) error {
   // Verify both attestations
   if err := v.verifyAttestation(ctx, attestations[0], region); err != nil {
       return err
   }
   if err := v.verifyAttestation(ctx, attestations[1], region); err != nil {
       return err
   }

   // Verify attestations match
   if attestations[0].Timestamp != attestations[1].Timestamp {
       return ErrInvalidAttestation
   }
   if !bytes.Equal(attestations[0].Data, attestations[1].Data) {
       return ErrInvalidAttestation
   }

   return nil
}

func (v *StateVerifier) VerifyStateTransition(ctx context.Context, action chain.Action) error {
   switch a := action.(type) {
   case *actions.CreateObjectAction:
       return v.verifyCreateObject(ctx, a)
   case *actions.SendEventAction:
       return v.verifyEvent(ctx, a)
   case *actions.SetInputObjectAction:
       return v.verifySetInputObject(ctx, a)
   case *actions.CreateRegionAction:
       return v.verifyCreateRegion(ctx, a)
   case *actions.UpdateRegionAction:
       return v.verifyUpdateRegion(ctx, a)
   default:
       return fmt.Errorf("unknown action type: %T", action)
   }
}

func (v *StateVerifier) verifyCreateObject(ctx context.Context, action *actions.CreateObjectAction) error {
   exists, err := storage.GetObject(ctx, v.state, action.ID)
   if err != nil {
       return err
   }
   if exists != nil {
       return actions.ErrObjectExists
   }

   obj := map[string][]byte{
       "code":    action.Code,
       "storage": action.Storage,
   }
   return v.VerifyObjectState(ctx, obj)
}

func (v *StateVerifier) verifyEvent(ctx context.Context, action *actions.SendEventAction) error {
   targetObj, err := storage.GetObject(ctx, v.state, action.IDTo)
   if err != nil {
       return err
   }
   if targetObj == nil {
       return actions.ErrObjectNotFound
   }

   // Get region for TEE verification
   regionID := extractRegionFromID(action.IDTo)
   region, err := storage.GetRegion(ctx, v.state, regionID)
   if err != nil {
       return err
   }
   if region == nil {
       return actions.ErrRegionNotFound
   }

   // Verify attestations
   if err := v.verifyAttestationPair(ctx, action.Attestations, region); err != nil {
       return err
   }

   // Verify function exists
   if err := v.verifyFunctionExists(targetObj, action.FunctionCall); err != nil {
       return err
   }

   if len(action.Parameters) > consts.MaxStorageSize {
       return actions.ErrStorageTooLarge
   }

   return nil
}

func (v *StateVerifier) verifySetInputObject(ctx context.Context, action *actions.SetInputObjectAction) error {
   obj, err := storage.GetObject(ctx, v.state, action.ID)
   if err != nil {
       return err
   }
   if obj == nil {
       return actions.ErrObjectNotFound
   }
   return nil
}

func (v *StateVerifier) verifyCreateRegion(ctx context.Context, action *actions.CreateRegionAction) error {
   region, err := storage.GetRegion(ctx, v.state, action.RegionID)
   if err != nil {
       return err
   }
   if region != nil {
       return actions.ErrRegionExists
   }

   // Verify all TEEs
   for _, tee := range action.TEEs {
       if len(tee) == 0 {
           return actions.ErrInvalidTEE
       }
   }

   // Verify attestations
   dummyRegion := map[string]interface{}{
       "tees": action.TEEs,
   }
   return v.verifyAttestationPair(ctx, action.Attestations, dummyRegion)
}

func (v *StateVerifier) verifyUpdateRegion(ctx context.Context, action *actions.UpdateRegionAction) error {
   region, err := storage.GetRegion(ctx, v.state, action.RegionID)
   if err != nil {
       return err
   }
   if region == nil {
       return actions.ErrRegionNotFound
   }

   // Verify attestations
   if err := v.verifyAttestationPair(ctx, action.Attestations, region); err != nil {
       return err
   }

   // Verify new TEEs
   for _, tee := range action.AddTEEs {
       if len(tee) == 0 {
           return actions.ErrInvalidTEE
       }
   }

   return nil
}

func (v *StateVerifier) verifyFunctionExists(obj map[string][]byte, function string) error {
   // Implementation would check if the function exists in the object's code
   return nil
}

func isTimeInWindow(timestamp, currentTime string) bool {
   // Implementation would verify timestamp is within acceptable window
   return true
}

func extractRegionFromID(id string) string {
   // Implementation would extract region ID from object ID
   return ""
}
