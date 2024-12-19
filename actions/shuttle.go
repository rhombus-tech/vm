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

func (a *CreateObjectAction) Execute(ctx context.Context, vm chain.VM) (*CreateObjectResult, error) {
    key := []byte("object:" + a.ID)
    
    exists, err := vm.State().Has(ctx, key)
    if err != nil {
        return nil, err
    }
    if exists {
        return nil, ErrObjectExists
    }
    
    obj := map[string][]byte{
        "code":    a.Code,
        "storage": a.Storage,
    }
    
    objBytes, err := codec.Marshal(obj)
    if err != nil {
        return nil, err
    }
    
    err = vm.State().Set(ctx, key, objBytes)
    if err != nil {
        return nil, err
    }
    
    return &CreateObjectResult{
        ID: a.ID,
    }, nil
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

// DeleteObjectAction removes an object from the VM
type DeleteObjectAction struct {
    ID string `json:"id"`
}

func (*DeleteObjectAction) GetTypeID() uint8 { return DeleteObject }

func (a *DeleteObjectAction) Marshal(p *codec.Packer) {
    p.PackString(a.ID)
}

func (a *DeleteObjectAction) Verify(ctx context.Context, vm chain.VM) error {
    if len(a.ID) == 0 || len(a.ID) > 256 {
        return ErrInvalidID
    }

    if exists, err := objectExists(ctx, vm, a.ID); err != nil {
        return err
    } else if !exists {
        return ErrObjectNotFound
    }

    // Only super object can delete objects
    if err := verifySuperObjectCaller(ctx, vm); err != nil {
        return ErrUnauthorized
    }

    return nil
}

func (a *DeleteObjectAction) Execute(ctx context.Context, vm chain.VM) (*DeleteObjectResult, error) {
    key := []byte("object:" + a.ID)
    
    exists, err := vm.State().Has(ctx, key)
    if err != nil {
        return nil, err
    }
    if !exists {
        return &DeleteObjectResult{
            ID:      a.ID,
            Success: false,
        }, ErrObjectNotFound
    }
    
    if err := vm.State().Remove(ctx, key); err != nil {
        return &DeleteObjectResult{
            ID:      a.ID,
            Success: false,
        }, err
    }
    
    return &DeleteObjectResult{
        ID:      a.ID,
        Success: true,
    }, nil
}

func UnmarshalDeleteObject(p *codec.Packer) (chain.Action, error) {
    var act DeleteObjectAction
    id, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.ID = id
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

func (a *SendEventAction) Execute(ctx context.Context, vm chain.VM) (*SendEventResult, error) {
    key := []byte("object:" + a.IDTo)
    objBytes, err := vm.State().Get(ctx, key)
    if err != nil {
        return nil, err
    }
    if objBytes == nil {
        return nil, ErrObjectNotFound
    }
    
    var obj map[string][]byte
    if err := codec.Unmarshal(objBytes, &obj); err != nil {
        return nil, err
    }
    
    event := map[string]interface{}{
        "priority":      a.Priority,
        "function_call": a.FunctionCall,
        "parameters":    a.Parameters,
    }
    
    eventBytes, err := codec.Marshal(event)
    if err != nil {
        return nil, err
    }
    
    queueKey := []byte(fmt.Sprintf("event:%d:%s", a.Priority, a.IDTo))
    if err := vm.State().Set(ctx, queueKey, eventBytes); err != nil {
        return nil, err
    }
    
    return &SendEventResult{
        Success: true,
        IDTo:    a.IDTo,
    }, nil
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

func (a *ChangeObjectCodeAction) Execute(ctx context.Context, vm chain.VM) (*ChangeObjectCodeResult, error) {
    if err := verifySuperObjectCaller(ctx, vm); err != nil {
        return &ChangeObjectCodeResult{
            ID:      a.ID,
            Success: false,
        }, ErrUnauthorized
    }
    
    key := []byte("object:" + a.ID)
    objBytes, err := vm.State().Get(ctx, key)
    if err != nil {
        return nil, err
    }
    if objBytes == nil {
        return &ChangeObjectCodeResult{
            ID:      a.ID,
            Success: false,
        }, ErrObjectNotFound
    }
    
    var obj map[string][]byte
    if err := codec.Unmarshal(objBytes, &obj); err != nil {
        return nil, err
    }
    
    obj["code"] = a.NewCode
    
    newObjBytes, err := codec.Marshal(obj)
    if err != nil {
        return nil, err
    }
    
    if err := vm.State().Set(ctx, key, newObjBytes); err != nil {
        return nil, err
    }
    
    return &ChangeObjectCodeResult{
        ID:      a.ID,
        Success: true,
    }, nil
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

// ChangeObjectStorageAction changes an object's storage
type ChangeObjectStorageAction struct {
    ID         string `json:"id"`
    NewStorage []byte `json:"new_storage"`
}

func (*ChangeObjectStorageAction) GetTypeID() uint8 { return ChangeObjectStorage }

func (a *ChangeObjectStorageAction) Marshal(p *codec.Packer) {
    p.PackString(a.ID)
    p.PackBytes(a.NewStorage)
}

func (a *ChangeObjectStorageAction) Verify(ctx context.Context, vm chain.VM) error {
    if len(a.ID) == 0 || len(a.ID) > 256 {
        return ErrInvalidID
    }

    if exists, err := objectExists(ctx, vm, a.ID); err != nil {
        return err
    } else if !exists {
        return ErrObjectNotFound
    }

    if len(a.NewStorage) > MaxStorageSize {
        return ErrStorageTooLarge
    }

    return nil
}

func (a *ChangeObjectStorageAction) Execute(ctx context.Context, vm chain.VM) (*ChangeObjectStorageResult, error) {
    key := []byte("object:" + a.ID)
    
    objBytes, err := vm.State().Get(ctx, key)
    if err != nil {
        return nil, err
    }
    if objBytes == nil {
        return &ChangeObjectStorageResult{
            ID:      a.ID,
            Success: false,
        }, ErrObjectNotFound
    }
    
    var obj map[string][]byte
    if err := codec.Unmarshal(objBytes, &obj); err != nil {
        return nil, err
    }
    
    obj["storage"] = a.NewStorage
    
    newObjBytes, err := codec.Marshal(obj)
    if err != nil {
        return nil, err
    }
    
    if err := vm.State().Set(ctx, key, newObjBytes); err != nil {
        return nil, err
    }
    
    return &ChangeObjectStorageResult{
        ID:      a.ID,
        Success: true,
    }, nil
}

func UnmarshalChangeObjectStorage(p *codec.Packer) (chain.Action, error) {
    var act ChangeObjectStorageAction
    
    id, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.ID = id
    
    newStorage, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.NewStorage = newStorage
    
    return &act, nil
}

// SetInputObjectAction designates the object that will receive extrinsics
type SetInputObjectAction struct {
    ID string `json:"id"`
}

func (*SetInputObjectAction) GetTypeID() uint8 { return SetInputObject }

func (a *SetInputObjectAction) Marshal(p *codec.Packer) {
    p.PackString(a.ID)
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

    // Only super object can set input object
    if err := verifySuperObjectCaller(ctx, vm); err != nil {
        return ErrUnauthorized
    }

    return nil
}

func (a *SetInputObjectAction) Execute(ctx context.Context, vm chain.VM) (*SetInputObjectResult, error) {
    key := []byte("input_object")
    
    if err := vm.State().Set(ctx, key, []byte(a.ID)); err != nil {
        return &SetInputObjectResult{
            ID:      a.ID,
            Success: false,
        }, err
    }
    
    return &SetInputObjectResult{
        ID:      a.ID,
        Success: true,
    }, nil
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

type DeleteObjectResult struct {
    ID      string `json:"id"`
    Success bool   `json:"success"`
}

func (*DeleteObjectResult) GetTypeID() uint8 { return DeleteObject }

func (r *DeleteObjectResult) Marshal(p *codec.Packer) {
    p.PackString(r.ID)
    p.PackBool(r.Success)
}

func UnmarshalDeleteObjectResult(p *codec.Packer) (codec.Typed, error) {
    var res DeleteObjectResult
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

type ChangeObjectCodeResult struct {
    ID      string `json:"id"`
    Success bool   `json:"success"`
}

func (*ChangeObjectCodeResult) GetTypeID() uint8 { return ChangeObjectCode }

func (r *ChangeObjectCodeResult) Marshal(p *codec.Packer) {
    p.PackString(r.ID)
    p.PackBool(r.Success)
}

func UnmarshalChangeObjectCodeResult(p *codec.Packer) (codec.Typed, error) {
    var res ChangeObjectCodeResult
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

type ChangeObjectStorageResult struct {
    ID      string `json:"id"`
    Success bool   `json:"success"`
}

func (*ChangeObjectStorageResult) GetTypeID() uint8 { return ChangeObjectStorage }

func (r *ChangeObjectStorageResult) Marshal(p *codec.Packer) {
    p.PackString(r.ID)
    p.PackBool(r.Success)
}

func UnmarshalChangeObjectStorageResult(p *codec.Packer) (codec.Typed, error) {
    var res ChangeObjectStorageResult
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
    // Implement code validation logic
    return nil
}

func validateFunctionExists(ctx context.Context, vm chain.VM, objectID, function string) error {
    // Implement function existence check
    return nil
}

func verifySuperObjectCaller(ctx context.Context, vm chain.VM) error {
    // Implement super object verification
    return nil
}

// RegisterActions registers all ShuttleVM actions with the auth factory
func RegisterActions(f *chain.AuthFactory) {
    f.Register(&CreateObjectAction{}, UnmarshalCreateObject)
    f.Register(&DeleteObjectAction{}, UnmarshalDeleteObject)
    f.Register(&ChangeObjectCodeAction{}, UnmarshalChangeObjectCode)
    f.Register(&ChangeObjectStorageAction{}, UnmarshalChangeObjectStorage)
    f.Register(&SetInputObjectAction{}, UnmarshalSetInputObject)
    f.Register(&SendEventAction{}, UnmarshalSendEvent)
}
