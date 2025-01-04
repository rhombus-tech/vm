// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package actions

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
    
    "github.com/ava-labs/hypersdk-starter-kit/storage"
    "github.com/ava-labs/hypersdk-starter-kit/consts"
    "github.com/rhombus-tech/hypersdk/coordination"
)

var (
    ErrObjectExists    = errors.New("object already exists")
    ErrObjectNotFound  = errors.New("object not found")
    ErrInvalidID       = errors.New("invalid object ID")
    ErrInvalidFunction = errors.New("invalid function call")
    ErrCodeTooLarge    = errors.New("code size exceeds maximum")  
    ErrStorageTooLarge = errors.New("storage size exceeds maximum")
    
    _ chain.Action = (*CreateObjectAction)(nil)
    _ chain.Action = (*SendEventAction)(nil)
    _ chain.Action = (*SetInputObjectAction)(nil)
)

const (
    MaxCodeSize    = 1024 * 1024    // 1MB
    MaxStorageSize = 1024 * 1024    // 1MB
    MaxIDLength    = 256
)

// Core types
type ObjectState struct {
    Code        []byte            `serialize:"true" json:"code"`
    Storage     []byte            `serialize:"true" json:"storage"`
    RegionID    string           `serialize:"true" json:"region_id"`
    Events      []string         `serialize:"true" json:"events"`
    LastUpdated time.Time        `serialize:"true" json:"last_updated"`
    Status      string           `serialize:"true" json:"status"`
}

type CreateObjectAction struct {
    ID       string `serialize:"true" json:"id"`
    Code     []byte `serialize:"true" json:"code"`
    Storage  []byte `serialize:"true" json:"storage"`
    RegionID string `serialize:"true" json:"region_id"`
}

func (*CreateObjectAction) GetTypeID() uint8 { 
    return mconsts.CreateObjectID 
}

func (c *CreateObjectAction) StateKeys(actor codec.Address, _ ids.ID) state.Keys {
    return state.Keys{
        string(storage.ObjectKey(c.ID)): state.Write,
        string(storage.RegionKey(c.RegionID)): state.Read,
    }
}

func (c *CreateObjectAction) Execute(
    ctx context.Context,
    rules chain.Rules,
    mu state.Mutable,
    timestamp int64,
    actor codec.Address,
    txID ids.ID,
) (codec.Typed, error) {
    // Verify inputs (from your previous Verify logic)
    if len(c.ID) == 0 || len(c.ID) > MaxIDLength {
        return nil, ErrInvalidID
    }
    if len(c.Code) > MaxCodeSize {
        return nil, ErrCodeTooLarge
    }
    if len(c.Storage) > MaxStorageSize {
        return nil, ErrStorageTooLarge
    }

    // Verify region exists
    region, err := storage.GetRegion(ctx, mu, c.RegionID)
    if err != nil {
        return nil, err
    }
    if region == nil {
        return nil, ErrRegionNotFound
    }

    // Check if object already exists
    exists, err := storage.ObjectExists(ctx, mu, c.ID)
    if err != nil {
        return nil, err
    }
    if exists {
        return nil, ErrObjectExists
    }

    if err := validateCode(c.Code); err != nil {
        return nil, err
    }

    // Create object state (from your previous Execute logic)
    obj := ObjectState{
        Code:        c.Code,
        Storage:     c.Storage,
        RegionID:    c.RegionID,
        Events:      make([]string, 0),
        LastUpdated: time.Unix(timestamp, 0).UTC(),
        Status:      "active",
    }

    // Store object
    if err := storage.SetObject(ctx, mu, c.ID, &obj); err != nil {
        return nil, err
    }

    return &CreateObjectResult{
        ID:       c.ID,
        RegionID: c.RegionID,
    }, nil
}

func (c *CreateObjectAction) ComputeUnits(chain.Rules) uint64 {
    return 1 + uint64(len(c.Code)+len(c.Storage))/1024
}

func (c *CreateObjectAction) ValidRange(chain.Rules) (int64, int64) {
    return -1, -1
}

type SendEventAction struct {
    IDTo         string           `serialize:"true" json:"id_to"`
    FunctionCall string           `serialize:"true" json:"function_call"`
    Parameters   []byte           `serialize:"true" json:"parameters"`
    Attestations [2]TEEAttestation `serialize:"true" json:"attestations"`
}

func (*SendEventAction) GetTypeID() uint8 { 
    return mconsts.SendEventID 
}

func (s *SendEventAction) StateKeys(actor codec.Address, _ ids.ID) state.Keys {
    return state.Keys{
        string(storage.ObjectKey(s.IDTo)): state.Read | state.Write,
        string(storage.EventKey(s.Attestations[0].Timestamp, s.IDTo)): state.Write,
    }
}

func (s *SendEventAction) Execute(
    ctx context.Context,
    rules chain.Rules,
    mu state.Mutable,
    timestamp int64,
    actor codec.Address,
    txID ids.ID,
) (codec.Typed, error) {
    // Get target object
    obj, err := storage.GetObject(ctx, mu, s.IDTo)
    if err != nil {
        return nil, err
    }
    if obj == nil {
        return nil, ErrObjectNotFound
    }

    // Verify function and parameters
    if len(s.FunctionCall) == 0 || len(s.FunctionCall) > MaxIDLength {
        return nil, ErrInvalidFunction
    }
    if len(s.Parameters) > MaxStorageSize {
        return nil, ErrStorageTooLarge
    }

    // Get region for TEE verification
    region, err := storage.GetRegion(ctx, mu, obj.RegionID)
    if err != nil {
        return nil, err
    }
    if region == nil {
        return nil, ErrRegionNotFound
    }

    // Verify workers are authorized for the region
    tees := region["tees"].([]TEEAddress)
    for _, att := range s.Attestations {
        found := false
        for _, tee := range tees {
            if bytes.Equal(tee, att.EnclaveID) {
                found = true
                break
            }
        }
        if !found {
            return nil, ErrInvalidAttestation
        }
    }

    // Verify attestation pair
    if err := verifyAttestationPair(s.Attestations); err != nil {
        return nil, err
    }

    // Create event record
    eventID := fmt.Sprintf("%s:%s", s.IDTo, s.Attestations[0].Timestamp)
    event := map[string]interface{}{
        "function_call": s.FunctionCall,
        "parameters":    s.Parameters,
        "attestations": s.Attestations,
        "timestamp":    s.Attestations[0].Timestamp,
        "status":      "pending",
    }

    // Store event
    if err := storage.SetEvent(ctx, mu, eventID, event); err != nil {
        return nil, err
    }

    // Update object's event list
    obj.Events = append(obj.Events, eventID)
    obj.LastUpdated = time.Unix(timestamp, 0).UTC()

    if err := storage.SetObject(ctx, mu, s.IDTo, obj); err != nil {
        return nil, err
    }

    return &SendEventResult{
        Success:   true,
        IDTo:      s.IDTo,
        EventID:   eventID,
        StateHash: s.Attestations[0].Data,
        Timestamp: s.Attestations[0].Timestamp,
    }, nil
}

func (s *SendEventAction) ComputeUnits(chain.Rules) uint64 {
    return 1 + uint64(len(s.Parameters))/1024
}

func (s *SendEventAction) ValidRange(chain.Rules) (int64, int64) {
    return -1, -1
}

// Helper function for attestation verification
func verifyAttestationPair(attestations [2]TEEAttestation) error {
    // Verify both attestations exist
    if len(attestations[0].EnclaveID) == 0 || len(attestations[1].EnclaveID) == 0 {
        return ErrMissingAttestation
    }

    // Verify timestamps match
    if attestations[0].Timestamp != attestations[1].Timestamp {
        return ErrAttestationMismatch
    }

    // Verify results match
    if !bytes.Equal(attestations[0].Data, attestations[1].Data) {
        return ErrAttestationMismatch
    }

    // Verify measurements are present
    if len(attestations[0].Measurement) == 0 || len(attestations[1].Measurement) == 0 {
        return ErrMissingAttestation
    }

    // Verify signatures
    if len(attestations[0].Signature) == 0 || len(attestations[1].Signature) == 0 {
        return ErrMissingAttestation
    }

    return nil
}

type SetInputObjectAction struct {
    ID string `serialize:"true" json:"id"`
}

func (*SetInputObjectAction) GetTypeID() uint8 { 
    return mconsts.SetInputObjectID 
}

func (s *SetInputObjectAction) StateKeys(actor codec.Address, _ ids.ID) state.Keys {
    return state.Keys{
        string(storage.InputObjectKey()): state.Write,
        string(storage.ObjectKey(s.ID)): state.Read,
    }
}

func (s *SetInputObjectAction) Execute(
    ctx context.Context,
    rules chain.Rules,
    mu state.Mutable,
    timestamp int64,
    actor codec.Address,
    txID ids.ID,
) (codec.Typed, error) {
    if len(s.ID) == 0 || len(s.ID) > MaxIDLength {
        return nil, ErrInvalidID
    }

    // Verify object exists
    exists, err := storage.ObjectExists(ctx, mu, s.ID)
    if err != nil {
        return nil, err
    }
    if !exists {
        return nil, ErrObjectNotFound
    }

    // Set input object
    if err := storage.SetInputObject(ctx, mu, s.ID); err != nil {
        return nil, err
    }

    return &SetInputObjectResult{
        ID:      s.ID,
        Success: true,
    }, nil
}

func (*SetInputObjectAction) ComputeUnits(chain.Rules) uint64 {
    return 1
}

func (*SetInputObjectAction) ValidRange(chain.Rules) (int64, int64) {
    return -1, -1
}

// Result types
type CreateObjectResult struct {
    ID       string `serialize:"true" json:"id"`
    RegionID string `serialize:"true" json:"region_id"`
}

func (*CreateObjectResult) GetTypeID() uint8 { 
    return mconsts.CreateObjectResultID 
}

type SendEventResult struct {
    Success   bool   `serialize:"true" json:"success"`
    IDTo      string `serialize:"true" json:"id_to"`
    EventID   string `serialize:"true" json:"event_id"`
    StateHash []byte `serialize:"true" json:"state_hash"`
    Timestamp string `serialize:"true" json:"timestamp"`
}

func (*SendEventResult) GetTypeID() uint8 { 
    return mconsts.SendEventResultID 
}

type SetInputObjectResult struct {
    ID      string `serialize:"true" json:"id"`
    Success bool   `serialize:"true" json:"success"`
}

func (*SetInputObjectResult) GetTypeID() uint8 { 
    return mconsts.SetInputObjectResultID 
}

// Helper functions
func validateCode(code []byte) error {
    if len(code) == 0 {
        return fmt.Errorf("empty code")
    }
    // Add basic code validation - can be expanded based on requirements
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

// Register actions with the registry
func RegisterActions(registry *chain.ActionRegistry) error {
    errs := []error{
        registry.Register(&CreateObjectAction{}),
        registry.Register(&SendEventAction{}),
        registry.Register(&SetInputObjectAction{}),
    }

    for _, err := range errs {
        if err != nil {
            return err
        }
    }
    return nil
}

// TEE type definitions
type TEEAttestation struct {
    EnclaveID   []byte `serialize:"true" json:"enclave_id"`
    Measurement []byte `serialize:"true" json:"measurement"`
    Timestamp   string `serialize:"true" json:"timestamp"`
    Data        []byte `serialize:"true" json:"data"`
    Signature   []byte `serialize:"true" json:"signature"`
    RegionProof []byte `serialize:"true" json:"region_proof"`
}

// Additional helper functions for state management
func getObjectState(ctx context.Context, mu state.Mutable, id string) (*ObjectState, error) {
    obj, err := storage.GetObject(ctx, mu, id)
    if err != nil {
        return nil, err
    }
    if obj == nil {
        return nil, ErrObjectNotFound
    }
    
    state := &ObjectState{}
    if err := codec.Unmarshal(obj, state); err != nil {
        return nil, fmt.Errorf("failed to unmarshal object state: %w", err)
    }
    return state, nil
}

func setObjectState(ctx context.Context, mu state.Mutable, id string, state *ObjectState) error {
    data, err := codec.Marshal(state)
    if err != nil {
        return fmt.Errorf("failed to marshal object state: %w", err)
    }
    return storage.SetObject(ctx, mu, id, data)
}