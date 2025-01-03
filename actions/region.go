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
    "github.com/ava-labs/hypersdk/consts"
    "github.com/cloudflare/roughtime"
)

var (
    ErrRegionExists       = errors.New("region already exists")
    ErrRegionNotFound     = errors.New("region not found")
    ErrInvalidTEE        = errors.New("invalid TEE")
    ErrInvalidRegionID   = errors.New("invalid region ID")
    ErrInvalidAttestation = errors.New("invalid TEE attestation")
    ErrAttestationMismatch = errors.New("attestation pair mismatch")
)

// Core types
type TEEAttestation struct {
    EnclaveID    []byte
    Measurement  []byte 
    Timestamp    string
    Data         []byte
    Signature    []byte
}

type TEEAddress []byte

type RegionState struct {
    Workers     []TEEAddress          `json:"workers"`
    Objects     map[string]bool       `json:"objects"`    // Track objects in region
    LastUpdate  time.Time            `json:"last_update"`
    Status      string               `json:"status"`
}

// Create region action
type CreateRegionAction struct {
    RegionID     string
    Workers      []TEEAddress
    Attestations [2]TEEAttestation
}

const (
    CreateRegion uint8 = iota + 8 // Start at 8 since other actions use 0-7
    UpdateRegion
)

func (a *TEEAttestation) Marshal(p *codec.Packer) {
    p.PackBytes(a.EnclaveID)
    p.PackBytes(a.Measurement)
    p.PackString(a.Timestamp)
    p.PackBytes(a.Data)
    p.PackBytes(a.Signature)
}

func (*CreateRegionAction) GetTypeID() uint8 { return CreateRegion }

func (*CreateRegionAction) ComputeUnits(rules chain.Rules) uint64 {
    return 1
}

func (a *CreateRegionAction) Marshal(p *codec.Packer) {
    p.PackString(a.RegionID)
    p.PackUint32(uint32(len(a.Workers)))
    for _, worker := range a.Workers {
        p.PackBytes(worker)
    }
    for i := 0; i < 2; i++ {
        a.Attestations[i].Marshal(p)
    }
}

func (a *CreateRegionAction) Verify(ctx context.Context, vm chain.VM) error {
    if len(a.RegionID) == 0 {
        return ErrInvalidRegionID
    }
    if len(a.Workers) < 2 {
        return ErrInvalidTEE
    }
    for _, worker := range a.Workers {
        if len(worker) == 0 {
            return ErrInvalidTEE
        }
    }
    return verifyAttestationPair(a.Attestations)
}

func (a *CreateRegionAction) Execute(ctx context.Context, vm chain.VM) (*CreateRegionResult, error) {
    state, err := vm.State()
    if err != nil {
        return nil, err
    }
    
    key := []byte("region:" + a.RegionID)
    
    exists, err := state.Has(ctx, key)
    if err != nil {
        return nil, err
    }
    if exists {
        return nil, ErrRegionExists
    }

    region := RegionState{
        Workers:    a.Workers,
        Objects:    make(map[string]bool),
        LastUpdate: time.Now().UTC(),
        Status:     "active",
    }

    regionBytes, err := codec.MarshalJSON(region) // Use MarshalJSON instead of Marshal
    if err != nil {
        return nil, err
    }

    if err := state.Set(ctx, key, regionBytes); err != nil {
        return nil, err
    }

    return &CreateRegionResult{
        RegionID: a.RegionID,
        Status:   "active",
        Workers:  uint64(len(a.Workers)),
    }, nil
}

// Helper functions
func verifyAttestationPair(attestations [2]TEEAttestation) error {
    if len(attestations[0].EnclaveID) == 0 || len(attestations[1].EnclaveID) == 0 {
        return ErrInvalidAttestation
    }

    if attestations[0].Timestamp != attestations[1].Timestamp {
        return ErrAttestationMismatch
    }

    if !bytes.Equal(attestations[0].Data, attestations[1].Data) {
        return ErrAttestationMismatch
    }

    return verifyTimestamp(attestations[0].Timestamp)
}

func verifyTimestamp(timestamp string) error {
    ts, err := time.Parse(time.RFC3339, timestamp)
    if err != nil {
        return fmt.Errorf("invalid timestamp format: %w", err)
    }

    now := roughtime.Now()
    diff := now.Sub(ts)
    if diff > 5*time.Minute || diff < -5*time.Minute {
        return fmt.Errorf("timestamp outside valid range")
    }

    return nil
}

// Result types
type CreateRegionResult struct {
    RegionID string `json:"region_id"`
    Status   string `json:"status"`
    Workers  uint64 `json:"worker_count"`
}

func (*CreateRegionResult) GetTypeID() uint8 { return CreateRegion }

func GetRegion(ctx context.Context, vm chain.VM) (*RegionState, error) {
    state, err := vm.State()
    if err != nil {
        return nil, err
    }
    
    key := []byte("region:" + regionID)
    
    regionBytes, err := state.Get(ctx, key)
    if err != nil {
        return nil, err
    }
    if regionBytes == nil {
        return nil, ErrRegionNotFound
    }

    var region RegionState
    if err := codec.UnmarshalJSON(regionBytes, &region); err != nil {
        return nil, err
    }

    return &region, nil
}

func VerifyWorkerInRegion(ctx context.Context, vm chain.VM, worker TEEAddress, regionID string) error {
    region, err := GetRegion(ctx, vm, regionID)
    if err != nil {
        return err
    }

    for _, w := range region.Workers {
        if bytes.Equal(w, worker) {
            return nil
        }
    }

    return ErrInvalidTEE
}

func RegisterRegionObject(ctx context.Context, vm chain.VM, regionID string, objectID string) error {
    region, err := GetRegion(ctx, vm, regionID)
    if err != nil {
        return err
    }

    region.Objects[objectID] = true
    region.LastUpdate = time.Now().UTC()

    regionBytes, err := codec.Marshal(region)
    if err != nil {
        return err
    }

    return vm.State().Set(ctx, []byte("region:"+regionID), regionBytes)
}