package actions

import (
    "context"
    "errors"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/consts"
)

var (
    ErrObjectExists     = errors.New("object already exists")
    ErrObjectNotFound   = errors.New("object not found")
    ErrInvalidID        = errors.New("invalid object ID")
    ErrUnauthorized     = errors.New("unauthorized operation")
    ErrCodeTooLarge     = errors.New("code size exceeds maximum")
    ErrStorageTooLarge  = errors.New("storage size exceeds maximum")
    ErrInvalidFunction  = errors.New("invalid function call")
    
    MaxCodeSize    = 1024 * 1024    // 1MB
    MaxStorageSize = 1024 * 1024    // 1MB
)

const (
    CreateObject uint8 = iota
    DeleteObject
    ChangeObjectCode
    ChangeObjectStorage
    SetInputObject
    SendEvent
)

// CreateObjectAction creates a new object in the VM
type CreateObjectAction struct {
    ID      string `json:"id"`
    Code    []byte `json:"code"`
    Storage []byte `json:"storage"`
}

func (*CreateObjectAction) GetTypeID() uint8 { return CreateObject }

func (a *CreateObjectAction) Marshal(p *codec.Packer) {
    p.PackString(a.ID)
    p.PackBytes(a.Code)
    p.PackBytes(a.Storage)
}

func (a *CreateObjectAction) Verify(ctx context.Context, vm chain.VM) error {
    if len(a.ID) == 0 || len(a.ID) > 256 {
        return ErrInvalidID
    }

    if len(a.Code) > MaxCodeSize {
        return ErrCodeTooLarge
    }
    if len(a.Storage) > MaxStorageSize {
        return ErrStorageTooLarge
    }

    if exists, err := objectExists(ctx, vm, a.ID); err != nil {
        return err
    } else if exists {
        return ErrObjectExists
    }

    if err := validateCode(a.Code); err != nil {
        return err
    }

    return nil
}

func UnmarshalCreateObject(p *codec.Packer) (chain.Action, error) {
    var act CreateObjectAction
    id, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.ID = id
    
    code, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.Code = code
    
    storage, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.Storage = storage
    
    return &act, nil
}

// SendEventAction represents an event being sent to an object
type SendEventAction struct {
    Priority      uint64 `json:"priority"`
    IDTo          string `json:"id_to"`
    FunctionCall  string `json:"function_call"`
    Parameters    []byte `json:"parameters"`
}

func (*SendEventAction) GetTypeID() uint8 { return SendEvent }

func (a *SendEventAction) Marshal(p *codec.Packer) {
    p.PackUint64(a.Priority)
    p.PackString(a.IDTo)
    p.PackString(a.FunctionCall)
    p.PackBytes(a.Parameters)
}

func (a *SendEventAction) Verify(ctx context.Context, vm chain.VM) error {
    if exists, err := objectExists(ctx, vm, a.IDTo); err != nil {
        return err
    } else if !exists {
        return ErrObjectNotFound
    }

    if len(a.FunctionCall) == 0 || len(a.FunctionCall) > 256 {
        return ErrInvalidFunction
    }

    if len(a.Parameters) > MaxStorageSize {
        return ErrStorageTooLarge
    }

    if err := validateFunctionExists(ctx, vm, a.IDTo, a.FunctionCall); err != nil {
        return err
    }

    return nil
}

func UnmarshalSendEvent(p *codec.Packer) (chain.Action, error) {
    var act SendEventAction
    
    priority, err := p.UnpackUint64()
    if err != nil {
        return nil, err
    }
    act.Priority = priority
    
    idTo, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.IDTo = idTo
    
    functionCall, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.FunctionCall = functionCall
    
    parameters, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.Parameters = parameters
    
    return &act, nil
}

// ChangeObjectCodeAction changes an object's code
type ChangeObjectCodeAction struct {
    ID      string `json:"id"`
    NewCode []byte `json:"new_code"`
}

func (*ChangeObjectCodeAction) GetTypeID() uint8 { return ChangeObjectCode }

func (a *ChangeObjectCodeAction) Marshal(p *codec.Packer) {
    p.PackString(a.ID)
    p.PackBytes(a.NewCode)
}

func (a *ChangeObjectCodeAction) Verify(ctx context.Context, vm chain.VM) error {
    if exists, err := objectExists(ctx, vm, a.ID); err != nil {
        return err
    } else if !exists {
        return ErrObjectNotFound
    }

    if len(a.NewCode) > MaxCodeSize {
        return ErrCodeTooLarge
    }

    if err := verifySuperObjectCaller(ctx, vm); err != nil {
        return ErrUnauthorized
    }

    if err := validateCode(a.NewCode); err != nil {
        return err
    }

    return nil
}

func UnmarshalChangeObjectCode(p *codec.Packer) (chain.Action, error) {
    var act ChangeObjectCodeAction
    
    id, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.ID = id
    
    newCode, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.NewCode = newCode
    
    return &act, nil
}

// Helper functions for verification
func objectExists(ctx context.Context, vm chain.VM, id string) (bool, error) {
    key := []byte("object:" + id)
    return vm.State().Has(ctx, key)
}

func validateCode(code []byte) error {
    // Implement code validation logic
    // This would check the bytecode format, security rules, etc.
    return nil
}

func validateFunctionExists(ctx context.Context, vm chain.VM, objectID, function string) error {
    // Implement function existence check
    // This would verify the function exists in the object's code
    return nil
}

func verifySuperObjectCaller(ctx context.Context, vm chain.VM) error {
    // Implement super object verification
    // This would check if the transaction signer is the super object
    return nil
}

// RegisterActions registers all ShuttleVM actions with the auth factory
func RegisterActions(f *chain.AuthFactory) {
    f.Register(&CreateObjectAction{}, UnmarshalCreateObject)
    f.Register(&SendEventAction{}, UnmarshalSendEvent)
    f.Register(&ChangeObjectCodeAction{}, UnmarshalChangeObjectCode)
    // Register other actions...
}
