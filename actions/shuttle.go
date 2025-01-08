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
    
    "github.com/rhombus-tech/vm"         
    "github.com/rhombus-tech/vm/core"
    "github.com/rhombus-tech/vm/consts"
    "github.com/rhombus-tech/vm/coordination"
)

var (
    // Keep object-specific errors
    ErrObjectExists    = errors.New("object already exists")
    ErrObjectNotFound  = errors.New("object not found")
    ErrInvalidID       = errors.New("invalid object ID")
    ErrInvalidFunction = errors.New("invalid function call")
    ErrCodeTooLarge    = errors.New("code size exceeds maximum")  
    ErrStorageTooLarge = errors.New("storage size exceeds maximum")
    ErrInvalidAttestation = errors.New("invalid attestation")
    
    // Add region-specific error
    ErrRegionRequired  = errors.New("region ID required")
)

const (
    MaxCodeSize    = 1024 * 1024    // 1MB
    MaxStorageSize = 1024 * 1024    // 1MB
    MaxIDLength    = 256
)

// Ensure implementations
var (
    _ chain.Action = (*CreateObjectAction)(nil)
    _ chain.Action = (*SendEventAction)(nil)
    _ chain.Action = (*SetInputObjectAction)(nil)
    _ codec.Typed = (*CreateObjectResult)(nil)
    _ codec.Typed = (*SendEventResult)(nil)
    _ codec.Typed = (*SetInputObjectResult)(nil)
)

// Action types - add RegionID field
type CreateObjectAction struct {
    ID       string `serialize:"true" json:"id"`
    Code     []byte `serialize:"true" json:"code"`
    Storage  []byte `serialize:"true" json:"storage"`
    RegionID string `serialize:"true" json:"region_id"`
}

type SendEventAction struct {
    IDTo         string                  `serialize:"true" json:"id_to"`
    FunctionCall string                  `serialize:"true" json:"function_call"`
    Parameters   []byte                  `serialize:"true" json:"parameters"`
    Attestations [2]core.TEEAttestation `serialize:"true" json:"attestations"`
    RegionID     string                  `serialize:"true" json:"region_id"`
}

type SetInputObjectAction struct {
    ID       string `serialize:"true" json:"id"`
    RegionID string `serialize:"true" json:"region_id"` // Add RegionID
}

// Result types - add RegionID where missing
type CreateObjectResult struct {
    ID       string `serialize:"true" json:"id"`
    RegionID string `serialize:"true" json:"region_id"`
}

type SendEventResult struct {
    Success   bool   `serialize:"true" json:"success"`
    IDTo      string `serialize:"true" json:"id_to"`
    EventID   string `serialize:"true" json:"event_id"`
    StateHash []byte `serialize:"true" json:"state_hash"`
    Timestamp string `serialize:"true" json:"timestamp"`
    RegionID  string `serialize:"true" json:"region_id"` // Add RegionID
}

type SetInputObjectResult struct {
    ID       string `serialize:"true" json:"id"`
    Success  bool   `serialize:"true" json:"success"`
    RegionID string `serialize:"true" json:"region_id"` // Add RegionID
}

// Keep all TypeID implementations unchanged
func (*CreateObjectAction) GetTypeID() uint8 { return consts.CreateObjectID }
func (*SendEventAction) GetTypeID() uint8 { return consts.SendEventID }
func (*SetInputObjectAction) GetTypeID() uint8 { return consts.SetInputObjectID }
func (*CreateObjectResult) GetTypeID() uint8 { return consts.CreateObjectResultID }
func (*SendEventResult) GetTypeID() uint8 { return consts.SendEventResultID }
func (*SetInputObjectResult) GetTypeID() uint8 { return consts.SetInputObjectResultID }

// Update StateKeys to include region
func (c *CreateObjectAction) StateKeys(actor codec.Address) state.Keys {
    return state.Keys{
        string([]byte(fmt.Sprintf("r/%s/obj/%s", c.RegionID, c.ID))): state.Write,
        string([]byte(fmt.Sprintf("r/%s/info", c.RegionID))): state.Read,
    }
}

// Update Execute methods with region awareness
func (c *CreateObjectAction) Execute(
    ctx context.Context,
    rules chain.Rules,
    mu state.Mutable,
    timestamp int64,
    actor codec.Address,
    txID ids.ID,
) (codec.Typed, error) {
    if c.RegionID == "" {
        return nil, ErrRegionRequired
    }

    stateManager := mu.(vm.StateManager)
    
    // Keep existing validations
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
    region, err := stateManager.GetRegion(ctx, mu, c.RegionID)
    if err != nil {
        return nil, err
    }
    if region == nil {
        return nil, ErrRegionNotFound
    }

    // Use region-aware existence check
    exists, err := stateManager.ObjectExists(ctx, mu, c.ID, c.RegionID)
    if err != nil {
        return nil, err
    }
    if exists {
        return nil, ErrObjectExists
    }

    // Keep code validation
    if err := validateCode(c.Code); err != nil {
        return nil, err
    }

    obj := &core.ObjectState{
        Code:        c.Code,
        Storage:     c.Storage,
        RegionID:    c.RegionID,
        Events:      make([]string, 0),
        LastUpdated: time.Unix(timestamp, 0).UTC(),
        Status:      "active",
    }

    if err := stateManager.SetObject(ctx, mu, c.ID, obj); err != nil {
        return nil, err
    }

    return &CreateObjectResult{
        ID:       c.ID,
        RegionID: c.RegionID,
    }, nil
}

// Keep existing ComputeUnits and ValidRange
func (c *CreateObjectAction) ComputeUnits(chain.Rules) uint64 {
    return 1 + uint64(len(c.Code)+len(c.Storage))/1024
}

func (c *CreateObjectAction) ValidRange(chain.Rules) (int64, int64) {
    return -1, -1
}

func UnmarshalCreateObject(data []byte) (chain.Action, error) {
    p := codec.NewReader(data, len(data))
    c := &CreateObjectAction{}
    c.Unmarshal(p)
    if err := p.Err(); err != nil {
        return nil, err
    }
    return c, nil
}

func (c *CreateObjectAction) Unmarshal(p *codec.Packer) error {
    c.ID = p.UnpackString(false)
    // code bytes
    p.UnpackBytes(0 /*limit=0 means unbounded*/, false /*required?*/, &c.Code)
    // storage bytes
    p.UnpackBytes(0, false, &c.Storage)
    // region
    c.RegionID = p.UnpackString(false)

    return p.Err()
}

func ParseCreateObject(p *codec.Packer) (chain.Action, error) {
    c := &CreateObjectAction{}
    // c.Unmarshal returns an error (not two values)
    if err := c.Unmarshal(p); err != nil {
        return nil, err
    }
    return c, nil
}

// Update SendEventAction StateKeys
func (s *SendEventAction) StateKeys(actor codec.Address) state.Keys {
    return state.Keys{
        string([]byte(fmt.Sprintf("r/%s/obj/%s", s.RegionID, s.IDTo))): state.Read | state.Write,
        string([]byte(fmt.Sprintf("r/%s/evt/%s", s.RegionID, s.IDTo))): state.Write,
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
    if s.RegionID == "" {
        return nil, ErrRegionRequired
    }

    stateManager := mu.(vm.StateManager)

    // Get object using region-aware method
    obj, err := stateManager.GetObject(ctx, mu, s.IDTo, s.RegionID)
    if err != nil {
        return nil, err
    }
    if obj == nil {
        return nil, ErrObjectNotFound
    }

    // Keep existing validations
    if len(s.FunctionCall) == 0 || len(s.FunctionCall) > MaxIDLength {
        return nil, ErrInvalidFunction
    }
    if len(s.Parameters) > MaxStorageSize {
        return nil, ErrStorageTooLarge
    }

    // Verify regions match
    if obj.RegionID != s.RegionID {
        return nil, fmt.Errorf("object region %s does not match event region %s", 
            obj.RegionID, s.RegionID)
    }

    // Keep attestation verification
    if err := verifyAttestationPair(s.Attestations); err != nil {
        return nil, err
    }

    // Create event with region
    eventID := fmt.Sprintf("%s:%s", s.IDTo, s.Attestations[0].Timestamp.Format(time.RFC3339))
    event := &core.Event{
        FunctionCall: s.FunctionCall,
        Parameters:   s.Parameters,
        Attestations: s.Attestations,
        Timestamp:    s.Attestations[0].Timestamp.Format(time.RFC3339),
        Status:      "pending",
    }

    // Store event with region
    if err := stateManager.SetEvent(ctx, mu, eventID, event, s.RegionID); err != nil {
        return nil, err
    }

    // Update object events
    obj.Events = append(obj.Events, eventID)
    obj.LastUpdated = time.Unix(timestamp, 0).UTC()

    if err := stateManager.SetObject(ctx, mu, s.IDTo, obj); err != nil {
        return nil, err
    }

    return &SendEventResult{
        Success:   true,
        IDTo:      s.IDTo,
        EventID:   eventID,
        StateHash: s.Attestations[0].Data,
        Timestamp: s.Attestations[0].Timestamp.UTC().Format(time.RFC3339),
        RegionID:  s.RegionID,
    }, nil
}


func (s *SendEventAction) ComputeUnits(chain.Rules) uint64 {
    return 1 + uint64(len(s.Parameters))/1024
}

func (s *SendEventAction) ValidRange(chain.Rules) (int64, int64) {
    return -1, -1
}

func ParseSendEvent(p *codec.Packer) (chain.Action, error) {
    s := &SendEventAction{}
    if err := s.Unmarshal(p); err != nil {
        return nil, err
    }
    return s, nil
}

// UnmarshalSendEvent is the function your parser calls to rebuild from bytes.
func UnmarshalSendEvent(data []byte) (chain.Action, error) {
    p := codec.NewReader(data, len(data))
    s := &SendEventAction{}
    s.Unmarshal(p)
    if err := p.Err(); err != nil {
        return nil, err
    }
    return s, nil
}

func (s *SendEventAction) Unmarshal(p *codec.Packer) error {
    s.IDTo = p.UnpackString(false)
    s.FunctionCall = p.UnpackString(false)
    p.UnpackBytes(0, false, &s.Parameters)

    s.RegionID = p.UnpackString(false)
    return p.Err()
}

// StateKeys implementation for SetInputObjectAction
func (s *SetInputObjectAction) StateKeys(actor codec.Address) state.Keys {
    return state.Keys{
        string([]byte("input_object")): state.Write,
        string([]byte("object:" + s.ID)): state.Read,
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
    stateManager := mu.(vm.StateManager)

    if len(s.ID) == 0 || len(s.ID) > MaxIDLength {
        return nil, ErrInvalidID
    }

    // Change 1: Add s.RegionID to ObjectExists call
    exists, err := stateManager.ObjectExists(ctx, mu, s.ID, s.RegionID)
    if err != nil {
        return nil, err
    }
    if !exists {
        return nil, ErrObjectNotFound
    }

    // Change 2: Since SetInputObject is not in the interface, we can store it as a special object
    obj := &core.ObjectState{
        RegionID: s.RegionID,
        Status: "input",
        LastUpdated: time.Unix(timestamp, 0).UTC(),
    }
    if err := stateManager.SetObject(ctx, mu, "input:"+s.ID, obj); err != nil {
        return nil, err
    }

    return &SetInputObjectResult{
        ID:      s.ID,
        Success: true,
        RegionID: s.RegionID,
    }, nil
}
func (*SetInputObjectAction) ComputeUnits(chain.Rules) uint64 {
    return 1
}

func (*SetInputObjectAction) ValidRange(chain.Rules) (int64, int64) {
    return -1, -1
}

func UnmarshalSetInputObject(data []byte) (chain.Action, error) {
    p := codec.NewReader(data, len(data))
    a := &SetInputObjectAction{}
    a.Unmarshal(p)
    if err := p.Err(); err != nil {
        return nil, err
    }
    return a, nil
}

// Unmarshal
func (s *SetInputObjectAction) Unmarshal(p *codec.Packer) error {
    s.ID = p.UnpackString(false)
    return p.Err()
}

func ParseSetInputObject(p *codec.Packer) (chain.Action, error) {
    s := &SetInputObjectAction{}
    if err := s.Unmarshal(p); err != nil {
        return nil, err
    }
    return s, nil
}

// Helper functions
func verifyAttestationPair(attestations [2]core.TEEAttestation) error {
    // Verify both attestations exist and have valid enclave IDs
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

    // Verify signatures are present
    if len(attestations[0].Signature) == 0 || len(attestations[1].Signature) == 0 {
        return ErrMissingAttestation
    }

    return nil
}

// Code validation helpers
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

// State management helpers
func getObjectState(ctx context.Context, mu state.Mutable, id string, regionID string) (*core.ObjectState, error) {
    stateManager := mu.(vm.StateManager)
    
    // Change 3: Add regionID parameter to GetObject call
    obj, err := stateManager.GetObject(ctx, mu, id, regionID)
    if err != nil {
        return nil, err
    }
    if obj == nil {
        return nil, ErrObjectNotFound
    }
    return obj, nil
}

func setObjectState(ctx context.Context, mu state.Mutable, id string, state *core.ObjectState) error {
    stateManager := mu.(vm.StateManager)
    return stateManager.SetObject(ctx, mu, id, state)
}

// Additional helper for region verification
func verifyRegionTEEs(region map[string]interface{}, attestations [2]core.TEEAttestation) error {
    tees, ok := region["tees"].([]core.TEEAddress)
    if !ok {
        return fmt.Errorf("invalid region TEE format")
    }

    for _, att := range attestations {
        found := false
        for _, tee := range tees {
            if bytes.Equal(tee, att.EnclaveID) {
                found = true
                break
            }
        }
        if !found {
            return ErrInvalidAttestation
        }
    }
    return nil
}

// Timestamp validation helper
func validateTimestamp(timestamp string) error {
    ts, err := time.Parse(time.RFC3339, timestamp)
    if err != nil {
        return fmt.Errorf("invalid timestamp format: %w", err)
    }
    
    now := time.Now()
    diff := now.Sub(ts)
    if diff > 5*time.Minute || diff < -5*time.Minute {
        return fmt.Errorf("timestamp outside acceptable range")
    }
    
    return nil
}

