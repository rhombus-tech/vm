// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package actions

import (
    "context"
    "errors"
    "bytes"
    "encoding/json"
    "fmt"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
)

var (
    ErrRegionExists    = errors.New("region already exists")
    ErrRegionNotFound  = errors.New("region not found")
    ErrInvalidTEE      = errors.New("invalid TEE")
    ErrInvalidRegionID = errors.New("invalid region ID")
    
    ErrMissingAttestation  = errors.New("missing TEE attestation")
    ErrInvalidAttestation  = errors.New("invalid TEE attestation") 
    ErrAttestationMismatch = errors.New("attestation pair mismatch")
    ErrInvalidTimestamp    = errors.New("invalid timestamp")
)

const (
    CreateRegion uint8 = 6
    UpdateRegion uint8 = 7

    // State keys
    RegionPrefix = "region:"
    
    // TEE constants 
    TEEAttestationVersion = uint8(1)
    MinAttestationSize    = 64  // Minimum size of attestation in bytes
    MaxAttestationSize    = 1024 // Maximum size of attestation in bytes
)

// TEE types and methods
type TEEAddress []byte

type TEEAttestation struct {
    Version     uint8  `json:"version"`
    EnclaveID   []byte `json:"enclave_id"`  
    Measurement []byte `json:"measurement"`
    Timestamp   string `json:"timestamp"`
    Data        []byte `json:"data"`
    Signature   []byte `json:"signature"`
}

func (a *TEEAttestation) Verify() error {
    if len(a.EnclaveID) == 0 {
        return fmt.Errorf("%w: missing enclave ID", ErrInvalidAttestation)
    }
    if len(a.Measurement) == 0 {
        return fmt.Errorf("%w: missing measurement", ErrInvalidAttestation)
    }
    if len(a.Timestamp) == 0 {
        return fmt.Errorf("%w: missing timestamp", ErrInvalidAttestation)
    }
    if len(a.Data) == 0 {
        return fmt.Errorf("%w: missing data", ErrInvalidAttestation)
    }
    if len(a.Signature) < MinAttestationSize {
        return fmt.Errorf("%w: signature too small", ErrInvalidAttestation)
    }
    if len(a.Signature) > MaxAttestationSize {
        return fmt.Errorf("%w: signature too large", ErrInvalidAttestation)
    }
    return nil
}

func (a *TEEAttestation) Marshal(p *codec.Packer) {
    p.PackByte(a.Version)
    p.PackBytes(a.EnclaveID)
    p.PackBytes(a.Measurement)
    p.PackString(a.Timestamp)
    p.PackBytes(a.Data)
    p.PackBytes(a.Signature)
}

func UnmarshalAttestation(p *codec.Packer) (TEEAttestation, error) {
    var att TEEAttestation
    
    att.Version = p.UnpackByte()
    if p.Err() != nil {
        return att, p.Err()
    }
    if att.Version != TEEAttestationVersion {
        return att, fmt.Errorf("%w: invalid version", ErrInvalidAttestation)
    }

    att.EnclaveID = p.UnpackBytes()
    if p.Err() != nil {
        return att, p.Err()
    }

    att.Measurement = p.UnpackBytes()
    if p.Err() != nil {
        return att, p.Err()
    }

    att.Timestamp = p.UnpackString()
    if p.Err() != nil {
        return att, p.Err()
    }

    att.Data = p.UnpackBytes()
    if p.Err() != nil {
        return att, p.Err()
    }
    
    att.Signature = p.UnpackBytes()
    if p.Err() != nil {
        return att, p.Err()
    }

    if err := att.Verify(); err != nil {
        return att, err
    }

    return att, nil
}

// Region actions
type CreateRegionAction struct {
    RegionID string       `json:"region_id"`
    TEEs     []TEEAddress `json:"tees"`
    Attestations [2]TEEAttestation 
}

func (*CreateRegionAction) GetTypeID() uint8 { return CreateRegion }

func (*CreateRegionAction) ComputeUnits(chain.Rules) uint64 { return 1 }

func (*CreateRegionAction) StateKeys(auth codec.Address) state.Keys {
    return state.Keys{
        string([]byte(RegionPrefix)): state.Read | state.Write,
    }
}

func (a *CreateRegionAction) Marshal(p *codec.Packer) {
    p.PackString(a.RegionID)
    p.PackInt(uint32(len(a.TEEs)))
    for _, tee := range a.TEEs {
        p.PackBytes(tee)
    }
    a.Attestations[0].Marshal(p)
    a.Attestations[1].Marshal(p)
}

func UnmarshalCreateRegion(p *codec.Packer) (chain.Action, error) {
    var act CreateRegionAction
    
    act.RegionID = p.UnpackString()
    if p.Err() != nil {
        return nil, p.Err()
    }

    numTEEs := p.UnpackInt()
    if p.Err() != nil {
        return nil, p.Err()
    }

    act.TEEs = make([]TEEAddress, numTEEs)
    for i := uint32(0); i < numTEEs; i++ {
        teeBytes := p.UnpackBytes()
        if p.Err() != nil {
            return nil, p.Err()
        }
        act.TEEs[i] = teeBytes
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

    if err := verifyAttestationPair(act.Attestations); err != nil {
        return nil, err
    }

    return &act, nil
}

func (a *CreateRegionAction) Execute(
    ctx context.Context,
    r chain.Rules,
    mut state.Mutable,
    timestamp int64,
    auth codec.Address,
    txID ids.ID,
) (codec.Typed, error) {
    key := []byte(RegionPrefix + a.RegionID)
    
    val, err := mut.GetValue(ctx, key)
    if err != nil {
        return nil, err
    }
    if val != nil {
        return &CreateRegionResult{
            RegionID: a.RegionID,
            Success: false,
        }, ErrRegionExists
    }
    
    region := map[string]interface{}{
        "tees": a.TEEs,
        "attestations": a.Attestations,
    }
    
    regionBytes, err := json.Marshal(region)
    if err != nil {
        return nil, err
    }
    
    if err := mut.Insert(ctx, key, regionBytes); err != nil {
        return nil, err
    }
    
    return &CreateRegionResult{
        RegionID: a.RegionID,
        Success: true,
        StateHash: a.Attestations[0].Data,
        Timestamp: a.Attestations[0].Timestamp,
    }, nil
}

type UpdateRegionAction struct {
    RegionID string       `json:"region_id"`
    AddTEEs  []TEEAddress `json:"add_tees"`
    RemTEEs  []TEEAddress `json:"rem_tees"`
    Attestations [2]TEEAttestation
}

func (*UpdateRegionAction) GetTypeID() uint8 { return UpdateRegion }

func (*UpdateRegionAction) ComputeUnits(chain.Rules) uint64 { return 1 }

func (*UpdateRegionAction) StateKeys(auth codec.Address) state.Keys {
    return state.Keys{
        string([]byte(RegionPrefix)): state.Read | state.Write,
    }
}

func (a *UpdateRegionAction) Marshal(p *codec.Packer) {
    p.PackString(a.RegionID)
    p.PackInt(uint32(len(a.AddTEEs)))
    for _, tee := range a.AddTEEs {
        p.PackBytes(tee)
    }
    p.PackInt(uint32(len(a.RemTEEs)))
    for _, tee := range a.RemTEEs {
        p.PackBytes(tee)
    }
    a.Attestations[0].Marshal(p)
    a.Attestations[1].Marshal(p)
}

func UnmarshalUpdateRegion(p *codec.Packer) (chain.Action, error) {
    var act UpdateRegionAction
    
    act.RegionID = p.UnpackString()
    if p.Err() != nil {
        return nil, p.Err()
    }

    numAddTEEs := p.UnpackInt()
    if p.Err() != nil {
        return nil, p.Err()
    }
    
    act.AddTEEs = make([]TEEAddress, numAddTEEs)
    for i := uint32(0); i < numAddTEEs; i++ {
        teeBytes := p.UnpackBytes()
        if p.Err() != nil {
            return nil, p.Err()
        }
        act.AddTEEs[i] = teeBytes
    }

    numRemTEEs := p.UnpackInt()
    if p.Err() != nil {
        return nil, p.Err()
    }
    
    act.RemTEEs = make([]TEEAddress, numRemTEEs)
    for i := uint32(0); i < numRemTEEs; i++ {
        teeBytes := p.UnpackBytes()
        if p.Err() != nil {
            return nil, p.Err()
        }
        act.RemTEEs[i] = teeBytes
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

    if err := verifyAttestationPair(act.Attestations); err != nil {
        return nil, err
    }

    return &act, nil
}

func (a *UpdateRegionAction) Execute(
    ctx context.Context,
    r chain.Rules,
    mut state.Mutable,
    timestamp int64,
    auth codec.Address,
    txID ids.ID,
) (codec.Typed, error) {
    key := []byte(RegionPrefix + a.RegionID)
    
    regionBytes, err := mut.GetValue(ctx, key)
    if err != nil {
        return nil, err
    }
    if regionBytes == nil {
        return &UpdateRegionResult{
            RegionID: a.RegionID,
            Success: false,
        }, ErrRegionNotFound
    }
    
    var region map[string]interface{}
    if err := json.Unmarshal(regionBytes, &region); err != nil {
        return nil, err
    }
    
    currentTEEs := region["tees"].([]TEEAddress)
    
    // Remove TEEs
    for _, remTEE := range a.RemTEEs {
        for i, tee := range currentTEEs {
            if bytes.Equal(tee, remTEE) {
                currentTEEs = append(currentTEEs[:i], currentTEEs[i+1:]...)
                break
            }
        }
    }
    
    // Add new TEEs
    currentTEEs = append(currentTEEs, a.AddTEEs...)
    
    region["tees"] = currentTEEs
    region["attestations"] = a.Attestations
    
    newRegionBytes, err := json.Marshal(region)
    if err != nil {
        return nil, err
    }
    
    if err := mut.Insert(ctx, key, newRegionBytes); err != nil {
        return nil, err
    }
    
    return &UpdateRegionResult{
        RegionID: a.RegionID,
        Success: true,
        StateHash: a.Attestations[0].Data,
        Timestamp: a.Attestations[0].Timestamp,
    }, nil
}

type CreateRegionResult struct {
    RegionID   string `json:"region_id"`
    Success    bool   `json:"success"`
    StateHash  []byte `json:"state_hash"`
    Timestamp  string `json:"timestamp"`
}

func (*CreateRegionResult) GetTypeID() uint8 { return CreateRegion }

type UpdateRegionResult struct {
    RegionID   string `json:"region_id"`
    Success    bool   `json:"success"`
    StateHash  []byte `json:"state_hash"`
    Timestamp  string `json:"timestamp"`
}

func (*UpdateRegionResult) GetTypeID() uint8 { return UpdateRegion }

func (r *CreateRegionResult) Marshal(p *codec.Packer) {
    p.PackString(r.RegionID)
    p.PackBool(r.Success)
    p.PackBytes(r.StateHash)
    p.PackString(r.Timestamp)
}

func UnmarshalCreateRegionResult(p *codec.Packer) (codec.Typed, error) {
    var res CreateRegionResult
    
    res.RegionID = p.UnpackString()
    if p.Err() != nil {
        return nil, p.Err()
    }

    res.Success = p.UnpackBool()
    if p.Err() != nil {
        return nil, p.Err()
    }

    res.StateHash = p.UnpackBytes()
    if p.Err() != nil {
        return nil, p.Err()
    }

    res.Timestamp = p.UnpackString()
    if p.Err() != nil {
        return nil, p.Err()
    }
    
    return &res, nil
}

func (r *UpdateRegionResult) Marshal(p *codec.Packer) {
    p.PackString(r.RegionID)
    p.PackBool(r.Success)
    p.PackBytes(r.StateHash)
    p.PackString(r.Timestamp)
}

func UnmarshalUpdateRegionResult(p *codec.Packer) (codec.Typed, error) {
    var res UpdateRegionResult
    
    res.RegionID = p.UnpackString()
    if p.Err() != nil {
        return nil, p.Err()
    }

    res.Success = p.UnpackBool()
    if p.Err() != nil {
        return nil, p.Err()
    }

    res.StateHash = p.UnpackBytes()
    if p.Err() != nil {
        return nil, p.Err()
    }

    res.Timestamp = p.UnpackString()
    if p.Err() != nil {
        return nil, p.Err()
    }
    
    return &res, nil
}

// Helper functions for attestation verification
func verifyAttestationPair(attestations [2]TEEAttestation) error {
    // Verify each attestation individually
    if err := attestations[0].Verify(); err != nil {
        return err
    }
    if err := attestations[1].Verify(); err != nil {
        return err
    }

    // Verify attestations are from different enclaves
    if bytes.Equal(attestations[0].EnclaveID, attestations[1].EnclaveID) {
        return fmt.Errorf("%w: duplicate enclave", ErrAttestationMismatch)
    }

    // Verify timestamps match
    if attestations[0].Timestamp != attestations[1].Timestamp {
        return fmt.Errorf("%w: timestamp mismatch", ErrAttestationMismatch)
    }

    // Verify data matches
    if !bytes.Equal(attestations[0].Data, attestations[1].Data) {
        return fmt.Errorf("%w: data mismatch", ErrAttestationMismatch)
    }

    // Verify measurements are valid
    if err := verifyMeasurements(attestations[0].Measurement, attestations[1].Measurement); err != nil {
        return fmt.Errorf("%w: %s", ErrAttestationMismatch, err)
    }

    return nil
}

func verifyMeasurements(m1, m2 []byte) error {
    if len(m1) == 0 || len(m2) == 0 {
        return errors.New("empty measurement")
    }
    if len(m1) != len(m2) {
        return errors.New("measurement length mismatch")
    }
    // Additional measurement verification could be added here
    return nil
}

// Initialize registers actions with the registry
func Initialize(registry chain.Registry) error {
    if err := registry.RegisterAction(CreateRegion, &CreateRegionAction{}, UnmarshalCreateRegion); err != nil {
        return err
    }
    if err := registry.RegisterAction(UpdateRegion, &UpdateRegionAction{}, UnmarshalUpdateRegion); err != nil {
        return err
    }
    return nil
}