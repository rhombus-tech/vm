// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package actions

import (
    "context"
    "errors"
    "fmt"

    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/consts"
    "github.com/cloudflare/roughtime"
)

var (
    ErrObjectExists    = errors.New("object already exists")
    ErrObjectNotFound  = errors.New("object not found")
    ErrInvalidID       = errors.New("invalid object ID")
    ErrInvalidFunction = errors.New("invalid function call")
    ErrCodeTooLarge    = errors.New("code size exceeds maximum")
    ErrStorageTooLarge = errors.New("storage size exceeds maximum")
    
    MaxCodeSize    = 1024 * 1024    // 1MB
    MaxStorageSize = 1024 * 1024    // 1MB
)

const (
    CreateObject uint8 = iota
    SendEvent
    SetInputObject
)

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
    return validateCode(a.Code)
}

func (a *CreateObjectAction) Execute(ctx context.Context, vm chain.VM) (*CreateObjectResult, error) {
    key := []byte("object:" + a.ID)
    obj := map[string][]byte{
        "code":    a.Code,
        "storage": a.Storage,
    }
    objBytes, err := codec.Marshal(obj)
    if err != nil {
        return nil, err
    }
    if err := vm.State().Set(ctx, key, objBytes); err != nil {
        return nil, err
    }
    return &CreateObjectResult{ID: a.ID}, nil
}

type SendEventAction struct {
    IDTo         string `json:"id_to"`
    FunctionCall string `json:"function_call"`
    Parameters   []byte `json:"parameters"`
}

func (*SendEventAction) GetTypeID() uint8 { return SendEvent }

func (a *SendEventAction) Marshal(p *codec.Packer) {
    p.PackString(a.IDTo)
    p.PackString(a.FunctionCall)
    p.PackBytes(a.Parameters)
}

func UnmarshalSendEvent(p *codec.Packer) (chain.Action, error) {
    var act SendEventAction
    
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
    return validateFunctionExists(ctx, vm, a.IDTo, a.FunctionCall)
}

func (a *SendEventAction) Execute(ctx context.Context, vm chain.VM) (*SendEventResult, error) {
    key := []byte("object:" + a.IDTo)
    objBytes, err := vm.State().Get(ctx, key)
    if err != nil {
        return nil, err
    }
    if objBytes == nil {
        return nil, ErrObjectNotFound
    }
    
    event := map[string]interface{}{
        "function_call": a.FunctionCall,
        "parameters":    a.Parameters,
    }
    eventBytes, err := codec.Marshal(event)
    if err != nil {
        return nil, err
    }
    
    queueKey := []byte(fmt.Sprintf("event:%s:%s", roughtime.Now(), a.IDTo))
    if err := vm.State().Set(ctx, queueKey, eventBytes); err != nil {
        return nil, err
    }
    
    return &SendEventResult{Success: true, IDTo: a.IDTo}, nil
}

type SetInputObjectAction struct {
    ID string `json:"id"`
}

func (*SetInputObjectAction) GetTypeID() uint8 { return SetInputObject }

func (a *SetInputObjectAction) Marshal(p *codec.Packer) {
    p.PackString(a.ID)
}

func UnmarshalSetInputObject(p *codec.Packer) (chain.Action, error) {
    var act SetInputObjectAction
    id, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.ID = id
    return &act, nil
}

func (a *SetInputObjectAction) Verify(ctx context.Context, vm chain.VM) error {
    if len(a.ID) == 0 || len(a.ID) > 256 {
        return ErrInvalidID
    }
    if exists, err := objectExists(ctx, vm, a.ID); err != nil {
        return err
    } else if !exists {
        return ErrObjectNotFound
    }
    return nil
}

func (a *SetInputObjectAction) Execute(ctx context.Context, vm chain.VM) (*SetInputObjectResult, error) {
    key := []byte("input_object")
    if err := vm.State().Set(ctx, key, []byte(a.ID)); err != nil {
        return nil, err
    }
    return &SetInputObjectResult{ID: a.ID, Success: true}, nil
}

// Result types
type CreateObjectResult struct {
    ID string `json:"id"`
}

func (*CreateObjectResult) GetTypeID() uint8 { return CreateObject }

func (r *CreateObjectResult) Marshal(p *codec.Packer) {
    p.PackString(r.ID)
}

func UnmarshalCreateObjectResult(p *codec.Packer) (codec.Typed, error) {
    var res CreateObjectResult
    id, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    res.ID = id
    return &res, nil
}

type SendEventResult struct {
    Success bool   `json:"success"`
    IDTo    string `json:"id_to"`
}

func (*SendEventResult) GetTypeID() uint8 { return SendEvent }

func (r *SendEventResult) Marshal(p *codec.Packer) {
    p.PackBool(r.Success)
    p.PackString(r.IDTo)
}

func UnmarshalSendEventResult(p *codec.Packer) (codec.Typed, error) {
    var res SendEventResult
    success, err := p.UnpackBool()
    if err != nil {
        return nil, err
    }
    res.Success = success

    idTo, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    res.IDTo = idTo
    return &res, nil
}

type SetInputObjectResult struct {
    ID      string `json:"id"`
    Success bool   `json:"success"`
}

func (*SetInputObjectResult) GetTypeID() uint8 { return SetInputObject }

func (r *SetInputObjectResult) Marshal(p *codec.Packer) {
    p.PackString(r.ID)
    p.PackBool(r.Success)
}

func UnmarshalSetInputObjectResult(p *codec.Packer) (codec.Typed, error) {
    var res SetInputObjectResult
    id, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    res.ID = id

    success, err := p.UnpackBool()
    if err != nil {
        return nil, err
    }
    res.Success = success
    return &res, nil
}

// Helper functions
func objectExists(ctx context.Context, vm chain.VM, id string) (bool, error) {
    key := []byte("object:" + id)
    return vm.State().Has(ctx, key)
}

func validateCode(code []byte) error {
    return nil
}

func validateFunctionExists(ctx context.Context, vm chain.VM, objectID, function string) error {
    return nil
}

// RegisterActions registers core actions with the auth factory
func RegisterActions(f *chain.AuthFactory) {
    f.Register(&CreateObjectAction{}, UnmarshalCreateObject)
    f.Register(&SendEventAction{}, UnmarshalSendEvent)
    f.Register(&SetInputObjectAction{}, UnmarshalSetInputObject)
}
