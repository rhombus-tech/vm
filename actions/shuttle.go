// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package actions

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/rhombus-tech/hypersdk/coordination"
)

var (
    ErrObjectExists    = errors.New("object already exists")
    ErrObjectNotFound  = errors.New("object not found")
    ErrInvalidID       = errors.New("invalid object ID")
    ErrInvalidFunction = errors.New("invalid function call")
    ErrCodeTooLarge    = errors.New("code size exceeds maximum")  
    ErrStorageTooLarge = errors.New("storage size exceeds maximum")
)

const (
    MaxCodeSize    = 1024 * 1024    // 1MB
    MaxStorageSize = 1024 * 1024    // 1MB
    MaxIDLength    = 256

    CreateObject uint8 = iota
    SendEvent
    SetInputObject
)

// Core types
type ObjectState struct {
    Code        []byte            `json:"code"`
    Storage     []byte            `json:"storage"`
    RegionID    string           `json:"region_id"`
    Events      []string         `json:"events"`     // Event history
    LastUpdated time.Time        `json:"last_updated"`
    Status      string           `json:"status"`
}

type VM interface {
    chain.VM
    Coordinator() *coordination.Coordinator
}

type CreateObjectAction struct {
    ID       string `json:"id"`
    Code     []byte `json:"code"`
    Storage  []byte `json:"storage"`
    RegionID string `json:"region_id"`
}

func (*CreateObjectAction) GetTypeID() uint8 { return CreateObject }

func (a *CreateObjectAction) ComputeUnits(rules chain.Rules) uint64 {
    return 1 + uint64(len(a.Code)+len(a.Storage))/1024
}

func (a *CreateObjectAction) Marshal(p *codec.Packer) {
    p.PackString(a.ID)
    p.PackBytes(a.Code)
    p.PackBytes(a.Storage)
    p.PackString(a.RegionID)
}

func UnmarshalCreateObject(p *codec.Packer) (chain.Action, error) {
    var act CreateObjectAction
    
    var err error
    act.ID, err = p.UnpackString()
    if err != nil {
        return nil, err
    }
    
    if err := p.UnpackBytesInto(&act.Code); err != nil {
        return nil, err
    }
    
    if err := p.UnpackBytesInto(&act.Storage); err != nil {
        return nil, err
    }
    
    act.RegionID, err = p.UnpackString()
    if err != nil {
        return nil, err
    }
    
    return &act, nil
}

func (a *CreateObjectAction) Verify(ctx context.Context, vm chain.VM) error {
    if len(a.ID) == 0 || len(a.ID) > MaxIDLength {
        return ErrInvalidID
    }
    if len(a.Code) > MaxCodeSize {
        return ErrCodeTooLarge
    }
    if len(a.Storage) > MaxStorageSize {
        return ErrStorageTooLarge
    }

    // Verify region exists
    if _, err := GetRegion(ctx, vm, a.RegionID); err != nil {
        return err
    }

    // Check if object already exists
    state := vm.State()
    exists, err := state.Has(ctx, []byte("object:"+a.ID))
    if err != nil {
        return err
    }
    if exists {
        return ErrObjectExists
    }

    return validateCode(a.Code)
}

func (a *CreateObjectAction) Execute(ctx context.Context, vm chain.VM) (*CreateObjectResult, error) {
    state := vm.State()
    key := []byte("object:" + a.ID)

    obj := ObjectState{
        Code:        a.Code,
        Storage:     a.Storage,
        RegionID:    a.RegionID,
        Events:      make([]string, 0),
        LastUpdated: time.Now().UTC(),
        Status:      "active",
    }

    objBytes, err := codec.Marshal(obj)
    if err != nil {
        return nil, err
    }

    if err := state.Set(ctx, key, objBytes); err != nil {
        return nil, err
    }

    // Register object with region
    if err := RegisterRegionObject(ctx, vm, a.RegionID, a.ID); err != nil {
        // Cleanup on failure
        state.Remove(ctx, key)
        return nil, err
    }

    return &CreateObjectResult{
        ID:       a.ID,
        RegionID: a.RegionID,
    }, nil
}

type SendEventAction struct {
    IDTo         string           `json:"id_to"`
    FunctionCall string           `json:"function_call"`
    Parameters   []byte           `json:"parameters"`
    Attestations [2]TEEAttestation `json:"attestations"`
}

func (*SendEventAction) GetTypeID() uint8 { return SendEvent }

func (a *SendEventAction) ComputeUnits(rules chain.Rules) uint64 {
    return 1 + uint64(len(a.Parameters))/1024
}

func (a *SendEventAction) Marshal(p *codec.Packer) {
    p.PackString(a.IDTo)
    p.PackString(a.FunctionCall)
    p.PackBytes(a.Parameters)
    a.Attestations[0].Marshal(p)
    a.Attestations[1].Marshal(p)
}

func UnmarshalSendEvent(p *codec.Packer) (chain.Action, error) {
    var act SendEventAction
    
    var err error
    act.IDTo, err = p.UnpackString()
    if err != nil {
        return nil, err
    }
    
    act.FunctionCall, err = p.UnpackString()
    if err != nil {
        return nil, err
    }
    
    if err := p.UnpackBytesInto(&act.Parameters); err != nil {
        return nil, err
    }
    
    att0, err := UnmarshalAttestation(p)
    if err != nil {
        return nil, err
    }
    act.Attestations[0] = att0
    
    att1, err := UnmarshalAttestation(p)
    if err != nil {
        return nil, err
    }
    act.Attestations[1] = att1
    
    return &act, nil
}

func (a *SendEventAction) Verify(ctx context.Context, vm chain.VM) error {
    // Get target object
    state := vm.State()
    objBytes, err := state.Get(ctx, []byte("object:"+a.IDTo))
    if err != nil {
        return err
    }
    if objBytes == nil {
        return ErrObjectNotFound
    }

    var obj ObjectState
    if err := codec.Unmarshal(objBytes, &obj); err != nil {
        return err
    }

    // Verify function and parameters
    if len(a.FunctionCall) == 0 || len(a.FunctionCall) > MaxIDLength {
        return ErrInvalidFunction
    }
    if len(a.Parameters) > MaxStorageSize {
        return ErrStorageTooLarge
    }

    // Verify workers are authorized for the region
    for _, att := range a.Attestations {
        if err := VerifyWorkerInRegion(ctx, vm, att.EnclaveID, obj.RegionID); err != nil {
            return err
        }
    }

    // Verify attestation pair
    return verifyAttestationPair(a.Attestations)
}

func (a *SendEventAction) Execute(ctx context.Context, vm chain.VM) (*SendEventResult, error) {
    vmWithCoord, ok := vm.(VM)
    if !ok {
        return nil, fmt.Errorf("vm does not support coordination")
    }
    coord := vmWithCoord.Coordinator()
    if coord == nil {
        return nil, fmt.Errorf("coordinator not initialized")
    }

    // Get and update target object
    state := vm.State()
    objKey := []byte("object:" + a.IDTo)
    objBytes, err := state.Get(ctx, objKey)
    if err != nil {
        return nil, err
    }

    var obj ObjectState
    if err := codec.Unmarshal(objBytes, &obj); err != nil {
        return nil, err
    }

    // Create event record
    eventID := fmt.Sprintf("%s:%s", a.IDTo, a.Attestations[0].Timestamp)
    event := map[string]interface{}{
        "function_call": a.FunctionCall,
        "parameters":    a.Parameters,
        "attestations": a.Attestations,
        "timestamp":    a.Attestations[0].Timestamp,
        "status":      "pending",
    }

    eventBytes, err := codec.Marshal(event)
    if err != nil {
        return nil, err
    }

    // Store event
    eventKey := []byte("event:" + eventID)
    if err := state.Set(ctx, eventKey, eventBytes); err != nil {
        return nil, err
    }

    // Update object's event list
    obj.Events = append(obj.Events, eventID)
    obj.LastUpdated = time.Now().UTC()

    updatedObjBytes, err := codec.Marshal(obj)
    if err != nil {
        state.Remove(ctx, eventKey)
        return nil, err
    }

    if err := state.Set(ctx, objKey, updatedObjBytes); err != nil {
        state.Remove(ctx, eventKey)
        return nil, err
    }

    return &SendEventResult{
        Success:   true,
        IDTo:      a.IDTo,
        EventID:   eventID,
        StateHash: a.Attestations[0].Data,
        Timestamp: a.Attestations[0].Timestamp,
    }, nil
}

type SetInputObjectAction struct {
    ID string `json:"id"`
}

func (*SetInputObjectAction) GetTypeID() uint8 { return SetInputObject }

func (*SetInputObjectAction) ComputeUnits(rules chain.Rules) uint64 {
    return 1
}

func (a *SetInputObjectAction) Marshal(p *codec.Packer) {
    p.PackString(a.ID)
}

func UnmarshalSetInputObject(p *codec.Packer) (chain.Action, error) {
    var act SetInputObjectAction
    
    var err error
    act.ID, err = p.UnpackString()
    if err != nil {
        return nil, err
    }
    
    return &act, nil
}

func (a *SetInputObjectAction) Verify(ctx context.Context, vm chain.VM) error {
    state := vm.State()
    exists, err := state.Has(ctx, []byte("object:"+a.ID))
    if err != nil {
        return err
    }
    if !exists {
        return ErrObjectNotFound
    }
    return nil
}

func (a *SetInputObjectAction) Execute(ctx context.Context, vm chain.VM) (*SetInputObjectResult, error) {
    state := vm.State()
    key := []byte("input_object")
    if err := state.Set(ctx, key, []byte(a.ID)); err != nil {
        return nil, err
    }
    return &SetInputObjectResult{
        ID:      a.ID,
        Success: true,
    }, nil
}

// Result types
type CreateObjectResult struct {
    ID       string `json:"id"`
    RegionID string `json:"region_id"`
}

func (*CreateObjectResult) GetTypeID() uint8 { return CreateObject }

func (r *CreateObjectResult) Marshal(p *codec.Packer) {
    p.PackString(r.ID)
    p.PackString(r.RegionID)
}

type SendEventResult struct {
    Success   bool   `json:"success"`
    IDTo      string `json:"id_to"`
    EventID   string `json:"event_id"`
    StateHash []byte `json:"state_hash"`
    Timestamp string `json:"timestamp"`
}

func (*SendEventResult) GetTypeID() uint8 { return SendEvent }

func (r *SendEventResult) Marshal(p *codec.Packer) {
    p.PackBool(r.Success)
    p.PackString(r.IDTo)
    p.PackString(r.EventID)
    p.PackBytes(r.StateHash)
    p.PackString(r.Timestamp)
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

// Helper functions
func validateCode(code []byte) error {
    if len(code) == 0 {
        return fmt.Errorf("empty code")
    }
    // Add basic code validation - can be expanded based on requirements
    // This is a placeholder for more sophisticated validation
    if code[0] == 0x00 {
        return fmt.Errorf("invalid code start byte")
    }
    return nil
}

func validateFunctionExists(code []byte, functionName string) error {
    if len(code) == 0 {
        return fmt.Errorf("empty code")
    }
    // Basic function validation - placeholder for actual implementation
    // Real implementation would parse code and verify function exists
    return nil
}

// Task management helper
func submitTask(
    ctx context.Context,
    coord *coordination.Coordinator,
    task *coordination.Task,
    maxRetries int,
) error {
    var lastErr error
    for i := 0; i < maxRetries; i++ {
        if err := coord.SubmitTask(ctx, task); err != nil {
            lastErr = err
            time.Sleep(500 * time.Millisecond)
            continue
        }
        return nil
    }
    return fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}

// Result handling helpers
func UnmarshalCreateObjectResult(p *codec.Packer) (codec.Typed, error) {
    var res CreateObjectResult
    
    var err error
    res.ID, err = p.UnpackString()
    if err != nil {
        return nil, err
    }
    
    res.RegionID, err = p.UnpackString()
    if err != nil {
        return nil, err
    }
    
    return &res, nil
}

func UnmarshalSendEventResult(p *codec.Packer) (codec.Typed, error) {
    var res SendEventResult
    
    var err error
    res.Success, err = p.UnpackBool()
    if err != nil {
        return nil, err
    }
    
    res.IDTo, err = p.UnpackString()
    if err != nil {
        return nil, err
    }
    
    res.EventID, err = p.UnpackString()
    if err != nil {
        return nil, err
    }
    
    if err := p.UnpackBytesInto(&res.StateHash); err != nil {
        return nil, err
    }
    
    res.Timestamp, err = p.UnpackString()
    if err != nil {
        return nil, err
    }
    
    return &res, nil
}

func UnmarshalSetInputObjectResult(p *codec.Packer) (codec.Typed, error) {
    var res SetInputObjectResult
    
    var err error
    res.ID, err = p.UnpackString()
    if err != nil {
        return nil, err
    }
    
    res.Success, err = p.UnpackBool()
    if err != nil {
        return nil, err
    }
    
    return &res, nil
}

// Register all actions
func RegisterActions(authFactory chain.AuthFactory) {
    authFactory.Register(&CreateObjectAction{}, UnmarshalCreateObject)
    authFactory.Register(&SendEventAction{}, UnmarshalSendEvent)
    authFactory.Register(&SetInputObjectAction{}, UnmarshalSetInputObject)
}
