package verifier

import (
    "context"
    "errors"

    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/state"

    "github.com/rhombus-tech/vm/actions"
    "github.com/rhombus-tech/vm/consts"
    "github.com/rhombus-tech/vm/storage"
)

var (
    ErrInputObjectMissing = errors.New("input object not found")
    ErrInvalidEventOrder  = errors.New("invalid event order")
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

func (v *StateVerifier) VerifyStateTransition(ctx context.Context, action chain.Action) error {
    switch a := action.(type) {
    case *actions.CreateObjectAction:
        return v.verifyCreateObject(ctx, a)
    case *actions.SetInputObjectAction:
        return v.verifySetInputObject(ctx, a)
    case *actions.SendEventAction:
        return v.verifyEvent(ctx, a)
    default:
        return fmt.Errorf("unknown action type: %T", action)
    }
}

// Helper functions for specific verifications
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

func (v *StateVerifier) verifyEvent(ctx context.Context, action *actions.SendEventAction) error {
    targetObj, err := storage.GetObject(ctx, v.state, action.IDTo)
    if err != nil {
        return err
    }
    if targetObj == nil {
        return actions.ErrObjectNotFound
    }

    if err := v.verifyFunctionExists(targetObj, action.FunctionCall); err != nil {
        return err
    }

    if len(action.Parameters) > consts.MaxStorageSize {
        return actions.ErrStorageTooLarge
    }

    return nil
}

func (v *StateVerifier) verifyFunctionExists(obj map[string][]byte, function string) error {
    // Implementation would check if the function exists in the object's code
    return nil
}

// New verifier methods for region actions
func (v *StateVerifier) VerifyCreateRegion(ctx context.Context, action *actions.CreateRegionAction) error {
   // Verify region doesn't already exist
   region, err := storage.GetRegion(ctx, v.state, action.RegionID)
   if err != nil {
       return err
   }
   if region != nil {
       return actions.ErrRegionExists
   }

   // Verify TEE list is valid
   if len(action.TEEs) == 0 {
       return actions.ErrInvalidTEE
   }
   for _, tee := range action.TEEs {
       if len(tee) == 0 {
           return actions.ErrInvalidTEE
       }
       // Could add more TEE validation here
   }

   return nil
}

func (v *StateVerifier) VerifyUpdateRegion(ctx context.Context, action *actions.UpdateRegionAction) error {
   // Verify region exists
   region, err := storage.GetRegion(ctx, v.state, action.RegionID)
   if err != nil {
       return err
   }
   if region == nil {
       return actions.ErrRegionNotFound
   }

   // Verify TEE lists are valid
   if len(action.AddTEEs) == 0 && len(action.RemTEEs) == 0 {
       return actions.ErrInvalidTEE
   }

   // Verify new TEEs
   for _, tee := range action.AddTEEs {
       if len(tee) == 0 {
           return actions.ErrInvalidTEE
       }
       // Could add more TEE validation here
   }

   // Verify TEEs to remove exist in current list
   currentTEEs := region["tees"].([]actions.TEEAddress)
   for _, remTEE := range action.RemTEEs {
       found := false
       for _, tee := range currentTEEs {
           if bytes.Equal(tee, remTEE) {
               found = true
               break
           }
       }
       if !found {
           return actions.ErrInvalidTEE
       }
   }

   return nil
}

// Update VerifyStateTransition to include region actions
func (v *StateVerifier) VerifyStateTransition(ctx context.Context, action chain.Action) error {
   switch a := action.(type) {
   // Existing cases...
   case *actions.CreateRegionAction:
       return v.VerifyCreateRegion(ctx, a)
   case *actions.UpdateRegionAction:
       return v.VerifyUpdateRegion(ctx, a)
   default:
       return fmt.Errorf("unknown action type: %T", action)
   }
}
