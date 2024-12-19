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
    ErrSuperObjectMissing   = errors.New("super object not found")
    ErrInputObjectMissing   = errors.New("input object not found")
    ErrInvalidEventOrder    = errors.New("invalid event order")
    ErrInvalidStateChange   = errors.New("invalid state change")
    ErrInvalidOwnership     = errors.New("invalid object ownership")
    ErrCircularDependency   = errors.New("circular event dependency detected")
)

// StateVerifier handles all state verification logic
type StateVerifier struct {
    state state.Mutable
}

func New(state state.Mutable) *StateVerifier {
    return &StateVerifier{
        state: state,
    }
}

// VerifySystemState verifies core system components
func (v *StateVerifier) VerifySystemState(ctx context.Context) error {
    // Verify super object exists and is valid
    superObject, err := storage.GetObject(ctx, v.state, "super")
    if err != nil {
        return err
    }
    if superObject == nil {
        return ErrSuperObjectMissing
    }

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

// VerifyObjectState validates object properties and constraints
func (v *StateVerifier) VerifyObjectState(ctx context.Context, obj map[string][]byte) error {
    // Verify code size
    if code, exists := obj["code"]; exists {
        if len(code) > consts.MaxCodeSize {
            return actions.ErrCodeTooLarge
        }
    }

    // Verify storage size
    if storage, exists := obj["storage"]; exists {
        if len(storage) > consts.MaxStorageSize {
            return actions.ErrStorageTooLarge
        }
    }

    return nil
}

// VerifyEvent validates event properties and constraints
func (v *StateVerifier) VerifyEvent(ctx context.Context, event *actions.SendEventAction) error {
    // Verify target object exists
    targetObj, err := storage.GetObject(ctx, v.state, event.IDTo)
    if err != nil {
        return err
    }
    if targetObj == nil {
        return actions.ErrObjectNotFound
    }

    // Verify function exists in target object
    if err := v.verifyFunctionExists(targetObj, event.FunctionCall); err != nil {
        return err
    }

    // Verify parameters size
    if len(event.Parameters) > consts.MaxStorageSize {
        return actions.ErrStorageTooLarge
    }

    return nil
}

// VerifyStateTransition validates state changes
func (v *StateVerifier) VerifyStateTransition(ctx context.Context, action chain.Action) error {
    switch a := action.(type) {
    case *actions.CreateObjectAction:
        return v.verifyCreateObject(ctx, a)
    case *actions.DeleteObjectAction:
        return v.verifyDeleteObject(ctx, a)
    case *actions.ChangeObjectCodeAction:
        return v.verifyChangeCode(ctx, a)
    case *actions.ChangeObjectStorageAction:
        return v.verifyChangeStorage(ctx, a)
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
    // Check if object already exists
    exists, err := storage.GetObject(ctx, v.state, action.ID)
    if err != nil {
        return err
    }
    if exists != nil {
        return actions.ErrObjectExists
    }

    // Verify object properties
    obj := map[string][]byte{
        "code":    action.Code,
        "storage": action.Storage,
    }
    return v.VerifyObjectState(ctx, obj)
}

func (v *StateVerifier) verifyDeleteObject(ctx context.Context, action *actions.DeleteObjectAction) error {
    // Verify caller is super object
    if err := v.verifySuperObjectCaller(ctx); err != nil {
        return err
    }

    // Verify object exists
    obj, err := storage.GetObject(ctx, v.state, action.ID)
    if err != nil {
        return err
    }
    if obj == nil {
        return actions.ErrObjectNotFound
    }

    return nil
}

func (v *StateVerifier) verifyChangeCode(ctx context.Context, action *actions.ChangeObjectCodeAction) error {
    // Verify caller is super object
    if err := v.verifySuperObjectCaller(ctx); err != nil {
        return err
    }

    // Verify code size
    if len(action.NewCode) > consts.MaxCodeSize {
        return actions.ErrCodeTooLarge
    }

    return nil
}

func (v *StateVerifier) verifyChangeStorage(ctx context.Context, action *actions.ChangeObjectStorageAction) error {
    // Verify storage size
    if len(action.NewStorage) > consts.MaxStorageSize {
        return actions.ErrStorageTooLarge
    }

    return nil
}

func (v *StateVerifier) verifySetInputObject(ctx context.Context, action *actions.SetInputObjectAction) error {
    // Verify caller is super object
    if err := v.verifySuperObjectCaller(ctx); err != nil {
        return err
    }

    // Verify target object exists
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
    return v.VerifyEvent(ctx, action)
}

// Internal helper functions

func (v *StateVerifier) verifySuperObjectCaller(ctx context.Context) error {
    // Implementation would check if the transaction signer is the super object
    return nil
}

func (v *StateVerifier) verifyFunctionExists(obj map[string][]byte, function string) error {
    // Implementation would check if the function exists in the object's code
    return nil
}
