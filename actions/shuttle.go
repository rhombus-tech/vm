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
   
   // New attestation errors
   ErrMissingAttestation = errors.New("missing TEE attestation")
   ErrInvalidAttestation = errors.New("invalid TEE attestation")
   ErrAttestationMismatch = errors.New("attestation pair mismatch")
   ErrInvalidTimestamp      = errors.New("invalid roughtime stamp")
   
   MaxCodeSize    = 1024 * 1024    // 1MB
   MaxStorageSize = 1024 * 1024    // 1MB
)


// New attestation types
type TEEAttestation struct {
   EnclaveID    []byte
   Measurement  []byte 
   Timestamp    string
   Data         []byte
   Signature    []byte
   RegionProof  []byte // Add proof of region execution
}

func (a *TEEAttestation) Marshal(p *codec.Packer) {
   p.PackBytes(a.EnclaveID)
   p.PackBytes(a.Measurement)
   p.PackString(a.Timestamp)
   p.PackBytes(a.Data)
   p.PackBytes(a.Signature)
}

func UnmarshalAttestation(p *codec.Packer) (TEEAttestation, error) {
   var att TEEAttestation
   
   enclaveID, err := p.UnpackBytes()
   if err != nil {
       return att, err
   }
   att.EnclaveID = enclaveID
   
   measurement, err := p.UnpackBytes() 
   if err != nil {
       return att, err
   }
   att.Measurement = measurement
   
   timestamp, err := p.UnpackString()
   if err != nil {
       return att, err
   }
   att.Timestamp = timestamp
   
   data, err := p.UnpackBytes()
   if err != nil {
       return att, err
   }
   att.Data = data
   
   sig, err := p.UnpackBytes()
   if err != nil {
       return att, err
   }
   att.Signature = sig
   
   return att, nil
}

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

type ObjectState struct {
    Code         []byte
    Storage      []byte
    RegionID     string                // Region this object belongs to
    Coordination *CoordinationState    // Track coordination state
}

type CoordinationState struct {
    LastTaskID      string
    ActiveWorkers   []coordination.WorkerID
    PendingTasks    map[string]*coordination.Task
    LastAttestation time.Time
}

func (a *CreateObjectAction) Execute(ctx context.Context, vm chain.VM) (*CreateObjectResult, error) {
    // 1. Get coordinator through VM interface
    vmWithCoord, ok := vm.(VM)
    if !ok {
        return nil, fmt.Errorf("vm does not support coordination")
    }
    coord := vmWithCoord.Coordinator()
    if coord == nil {
        return nil, fmt.Errorf("coordinator not initialized")
    }

    // 2. Validate region first
    if err := validateRegion(ctx, vm, a.RegionID); err != nil {
        return nil, err
    }

    // 3. Create enhanced object structure with coordination info
    obj := map[string]interface{}{
        "code":    a.Code,
        "storage": a.Storage,
        "coordination": map[string]interface{}{
            "region_id": a.RegionID,
            "state": map[string]interface{}{
                "created_at":        time.Now().UTC().Format(time.RFC3339),
                "last_event":        "",
                "last_coordination": "",
                "status":           "initialized",
                "tasks":            make(map[string]interface{}),
                "access_log":       make([]string, 0), // Track worker access
            },
            "config": map[string]interface{}{
                "timeout":           5 * time.Second,
                "max_retries":       3,
                "sync_interval":     10 * time.Second,
                "heartbeat_enabled": true,
            },
        },
    }

    // 4. Get and configure region workers
    regionWorkers, err := getRegionWorkers(ctx, vm, a.RegionID)
    if err != nil {
        return nil, err
    }

    coordConfig := obj["coordination"].(map[string]interface{})
    coordConfig["allowed_workers"] = regionWorkers
    
    // 5. Store object state
    objBytes, err := codec.Marshal(obj)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal object: %w", err)
    }

    key := []byte("object:" + a.ID)
    if err := vm.State().Set(ctx, key, objBytes); err != nil {
        return nil, fmt.Errorf("failed to store object: %w", err)
    }

    // 6. Update coordination state atomically
    coordState, err := storage.GetCoordinationState(ctx, vm.State())
    if err != nil {
        vm.State().Remove(ctx, key)
        return nil, fmt.Errorf("failed to get coordination state: %w", err)
    }

    // Initialize region objects if needed
    if coordState.Regions == nil {
        coordState.Regions = make(map[string]map[string]storage.TaskState)
    }
    if coordState.Regions[a.RegionID] == nil {
        coordState.Regions[a.RegionID] = make(map[string]storage.TaskState)
    }

    // Add object to region tracking
    coordState.Regions[a.RegionID][a.ID] = storage.TaskState{
        Status:    "initialized",
        Workers:   regionWorkers,
        Timestamp: time.Now().UTC().Format(time.RFC3339),
        Config:    coordConfig["config"].(map[string]interface{}),
    }

    // Store updated coordination state
    if err := storage.SetCoordinationState(ctx, vm.State(), coordState); err != nil {
        vm.State().Remove(ctx, key)
        return nil, fmt.Errorf("failed to update coordination state: %w", err)
    }

    return &CreateObjectResult{
        ID:          a.ID,
        RegionID:    a.RegionID,
        StateHash:   coordState.StateHash(),
        WorkerCount: len(regionWorkers),
        Status:      "initialized",
    }, nil
}

// Helper functions for better organization
func validateRegion(ctx context.Context, vm chain.VM, regionID string) error {
    regionKey := []byte("region:" + regionID)
    exists, err := vm.State().Has(ctx, regionKey)
    if err != nil {
        return fmt.Errorf("failed to check region: %w", err)
    }
    if !exists {
        return ErrRegionNotFound
    }
    return nil
}

func getRegionWorkers(ctx context.Context, vm chain.VM, regionID string) ([]coordination.WorkerID, error) {
    regionKey := []byte("region:" + regionID)
    regionBytes, err := vm.State().Get(ctx, regionKey)
    if err != nil {
        return nil, fmt.Errorf("failed to get region: %w", err)
    }

    var region map[string]interface{}
    if err := codec.Unmarshal(regionBytes, &region); err != nil {
        return nil, fmt.Errorf("failed to unmarshal region: %w", err)
    }

    coordInfo, ok := region["coordination"].(map[string]interface{})
    if !ok {
        return nil, fmt.Errorf("invalid region coordination info")
    }

    workers, ok := coordInfo["workers"].([]coordination.WorkerID)
    if !ok || len(workers) == 0 {
        return nil, fmt.Errorf("no workers available in region")
    }

    return workers, nil
}

// Get worker IDs for a region
func getWorkerIDs(tees []TEEAddress) []coordination.WorkerID {
    ids := make([]coordination.WorkerID, len(tees))
    for i, tee := range tees {
        ids[i] = coordination.WorkerID(tee)
    }
    return ids
}

// Get active workers for a region
func getRegionWorkers(coord *coordination.Coordinator, regionID string) []coordination.WorkerID {
    workers := coord.GetWorkerIDs()
    var regionWorkers []coordination.WorkerID
    
    for _, worker := range workers {
        if isWorkerInRegion(worker, regionID) {
            regionWorkers = append(regionWorkers, worker)
        }
    }
    
    return regionWorkers
}

// Check if worker belongs to region
func isWorkerInRegion(worker coordination.WorkerID, regionID string) bool {
    // Implementation would check worker's region assignment
    // This is a placeholder
    return true
}

type SendEventAction struct {
    IDTo         string `json:"id_to"`
    FunctionCall string `json:"function_call"`
    Parameters   []byte `json:"parameters"`
    Attestations  [2]TEEAttestation // Paired TEE attestations
    RegionID     string // Add region identifier
}

func (*SendEventAction) GetTypeID() uint8 { return SendEvent }

func (a *SendEventAction) Marshal(p *codec.Packer) {
    p.PackString(a.IDTo)
    p.PackString(a.FunctionCall)
    p.PackBytes(a.Parameters)
    p.PackString(a.RegionID)  // Add this line
    a.Attestations[0].Marshal(p)
    a.Attestations[1].Marshal(p)
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

    // Add this block
    regionID, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.RegionID = regionID

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
    // 1. Get coordinator through VM interface
    vmWithCoord, ok := vm.(VM)
    if !ok {
        return fmt.Errorf("vm does not support coordination")
    }
    coord := vmWithCoord.Coordinator()
    if coord == nil {
        return fmt.Errorf("coordinator not initialized")
    }

    // 2. Basic validation
    if len(a.FunctionCall) == 0 || len(a.FunctionCall) > 256 {
        return ErrInvalidFunction
    }
    if len(a.Parameters) > MaxStorageSize {
        return ErrStorageTooLarge
    }

    // 3. Verify object exists and get its info
    objBytes, err := vm.State().Get(ctx, []byte("object:"+a.IDTo))
    if err != nil {
        return err
    }
    if objBytes == nil {
        return ErrObjectNotFound
    }

    var obj map[string]interface{}
    if err := codec.Unmarshal(objBytes, &obj); err != nil {
        return fmt.Errorf("failed to unmarshal object: %w", err)
    }

    // 4. Verify region assignments
    coordInfo, ok := obj["coordination"].(map[string]interface{})
    if !ok {
        return fmt.Errorf("invalid object coordination info")
    }

    regionID, ok := coordInfo["region_id"].(string)
    if !ok {
        return fmt.Errorf("invalid region assignment")
    }

    // 5. Verify workers are authorized for region
    for _, att := range a.Attestations {
        workerID := coordination.WorkerID(att.EnclaveID)
        if err := verifyWorkerInRegion(ctx, vm, workerID, regionID); err != nil {
            return fmt.Errorf("worker verification failed: %w", err)
        }
    }

    // 6. Verify attestation pair
    if err := verifyAttestationPair(a.Attestations); err != nil {
        return fmt.Errorf("attestation verification failed: %w", err)
    }

    // 7. Verify function exists 
    if err := validateFunctionExists(ctx, vm, a.IDTo, a.FunctionCall); err != nil {
        return fmt.Errorf("function validation failed: %w", err)
    }

    // 8. Verify worker states
    for _, att := range a.Attestations {
        workerID := coordination.WorkerID(att.EnclaveID)
        worker, exists := coord.GetWorker(workerID)
        if !exists {
            return fmt.Errorf("worker %s not registered", workerID)
        }
        if worker.Status == WorkerStatusError {
            return fmt.Errorf("worker %s in error state", workerID)
        }
    }

    return nil
}

// Helper function to verify worker is authorized for region
func verifyWorkerInRegion(ctx context.Context, vm chain.VM, workerID coordination.WorkerID, regionID string) error {
    regionBytes, err := vm.State().Get(ctx, []byte("region:"+regionID))
    if err != nil {
        return err
    }
    if regionBytes == nil {
        return ErrRegionNotFound
    }

    var region map[string]interface{}
    if err := codec.Unmarshal(regionBytes, &region); err != nil {
        return err
    }

    coordInfo, ok := region["coordination"].(map[string]interface{})
    if !ok {
        return fmt.Errorf("invalid region coordination info")
    }

    workers, ok := coordInfo["workers"].([]coordination.WorkerID)
    if !ok {
        return fmt.Errorf("invalid region workers")
    }

    for _, w := range workers {
        if w == workerID {
            return nil
        }
    }

    return fmt.Errorf("worker not authorized for region")
}

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

    // Basic timestamp validation
    if err := verifyTimestamp(attestations[0].Timestamp); err != nil {
        return err
    }

    return nil
}

func verifyTimestamp(timestamp string) error {
    // Add timestamp verification against roughtime
    return nil
}

func (a *SendEventAction) Execute(ctx context.Context, vm chain.VM) (*SendEventResult, error) {
    // 1. Verify object exists
    key := []byte("object:" + a.IDTo)
    objBytes, err := vm.State().Get(ctx, key)
    if err != nil {
        return nil, err
    }
    if objBytes == nil {
        return nil, ErrObjectNotFound
    }

    // 2. Get coordinator through VM interface - more modular approach
    vmWithCoord, ok := vm.(VM)
    if !ok {
        return nil, fmt.Errorf("vm does not support coordination")
    }
    coord := vmWithCoord.Coordinator()
    if coord == nil {
        return nil, fmt.Errorf("coordinator not initialized")
    }

    // 3. Create coordination task
    taskID := fmt.Sprintf("event:%s:%s", a.IDTo, a.Attestations[0].Timestamp)
    task := &coordination.Task{
        ID: taskID,
        WorkerIDs: []coordination.WorkerID{
            coordination.WorkerID(a.Attestations[0].EnclaveID),
            coordination.WorkerID(a.Attestations[1].EnclaveID),
        },
        Data: a.Parameters,
        Attestations: [][]byte{
            a.Attestations[0].EnclaveID, 
            a.Attestations[1].EnclaveID,
        },
        Timeout: 5 * time.Second,
    }

    // 4. Submit and verify task acceptance
    if err := coord.SubmitTask(ctx, task); err != nil {
        return nil, fmt.Errorf("coordination failed: %w", err)
    }

    // 5. Store enhanced event state
    event := map[string]interface{}{
        "function_call": a.FunctionCall,
        "parameters":    a.Parameters,
        "attestations": a.Attestations,
        "task_id":      taskID,
        "status":       "pending",
        "workers":      task.WorkerIDs,
        "timestamp":    a.Attestations[0].Timestamp,
        "coordination": map[string]interface{}{ // Add more coordination metadata
            "started_at": time.Now().UTC().Format(time.RFC3339),
            "timeout":    task.Timeout,
            "retries":   0,
        },
    }

    eventBytes, err := codec.Marshal(event)
    if err != nil {
        // Try to cancel task on error
        coord.CancelTask(ctx, taskID)
        return nil, err
    }

    // 6. Store in event queue
    queueKey := []byte(fmt.Sprintf("event:%s:%s", a.Attestations[0].Timestamp, a.IDTo))
    if err := vm.State().Set(ctx, queueKey, eventBytes); err != nil {
        // Try to cancel task on error
        coord.CancelTask(ctx, taskID)
        return nil, err
    }

    // 7. Return enhanced result
    return &SendEventResult{
        Success:   true,
        IDTo:      a.IDTo,
        StateHash: a.Attestations[0].Data,
        Timestamp: a.Attestations[0].Timestamp,
        TaskID:    taskID,
        Status:    "pending", // Add status for tracking
    }, nil
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

func (a *SendEventAction) Verify(ctx context.Context, vm chain.VM) error {
    // Original verification
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

    // Verify attestations
    if err := verifyAttestationPair(a.Attestations); err != nil {
        return err
    }

    return validateFunctionExists(ctx, vm, a.IDTo, a.FunctionCall)
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

// SendEventResult includes attestation verification results
type SendEventResult struct {
    Success    bool   `json:"success"`
    IDTo       string `json:"id_to"`
    StateHash  []byte `json:"state_hash"`    // From attestation
    Timestamp  string `json:"timestamp"`     // From attestation
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
