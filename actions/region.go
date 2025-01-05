package actions

import (
    "bytes"
    "context"
    "errors"
    "time"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
    
    "github.com/rhombus-tech/vm"       
    "github.com/rhombus-tech/vm/types" 
    "github.com/rhombus-tech/vm/consts"
)

var (
    ErrRegionExists       = errors.New("region already exists")
    ErrRegionNotFound     = errors.New("region not found")
    ErrInvalidTEE        = errors.New("invalid TEE")
    ErrInvalidRegionID   = errors.New("invalid region ID")
    ErrTooManyTEEs       = errors.New("too many TEEs")
    ErrMissingAttestation = errors.New("missing TEE attestation")
    ErrAttestationMismatch = errors.New("attestation pair mismatch")

    MaxTEEsPerRegion = 32
)

var (
    _ chain.Action = (*CreateRegionAction)(nil)
    _ chain.Action = (*UpdateRegionAction)(nil)
    _ codec.Typed = (*CreateRegionResult)(nil)
    _ codec.Typed = (*UpdateRegionResult)(nil)
)

type CreateRegionAction struct {
    RegionID     string                  `serialize:"true" json:"region_id"`
    TEEs         []types.TEEAddress      `serialize:"true" json:"tees"`
    Attestations [2]types.TEEAttestation `serialize:"true" json:"attestations"`
}

type UpdateRegionAction struct {
    RegionID     string                  `serialize:"true" json:"region_id"`
    AddTEEs      []types.TEEAddress      `serialize:"true" json:"add_tees"`
    RemTEEs      []types.TEEAddress      `serialize:"true" json:"rem_tees"`
    Attestations [2]types.TEEAttestation `serialize:"true" json:"attestations"`
}

type CreateRegionResult struct {
    RegionID  string `serialize:"true" json:"region_id"`
    Success   bool   `serialize:"true" json:"success"`
    StateHash []byte `serialize:"true" json:"state_hash"`
    Timestamp string `serialize:"true" json:"timestamp"`
}

type UpdateRegionResult struct {
    RegionID  string `serialize:"true" json:"region_id"`
    Success   bool   `serialize:"true" json:"success"`
    StateHash []byte `serialize:"true" json:"state_hash"`
    Timestamp string `serialize:"true" json:"timestamp"`
}

func (*CreateRegionAction) GetTypeID() uint8 {
    return consts.CreateRegionID
}

func (c *CreateRegionAction) StateKeys(actor codec.Address) state.Keys {
    return state.Keys{
        string([]byte("region:" + c.RegionID)): state.Write,
    }
}

func (c *CreateRegionAction) Execute(
    ctx context.Context,
    _ chain.Rules,
    mu state.Mutable,
    _ int64,
    actor codec.Address,
    _ ids.ID,
) (codec.Typed, error) {
    stateManager := mu.(vm.StateManager)

    if len(c.RegionID) == 0 || len(c.RegionID) > 256 {
        return nil, ErrInvalidRegionID
    }

    if len(c.TEEs) == 0 || len(c.TEEs) > MaxTEEsPerRegion {
        return nil, ErrTooManyTEEs
    }

    for _, tee := range c.TEEs {
        if len(tee) == 0 || len(tee) > 64 {
            return nil, ErrInvalidTEE
        }
    }

    if err := verifyAttestationPair(c.Attestations); err != nil {
        return nil, err
    }

    exists, err := stateManager.RegionExists(ctx, mu, c.RegionID)
    if err != nil {
        return nil, err
    }
    if exists {
        return nil, ErrRegionExists
    }

    region := map[string]interface{}{
        "tees": c.TEEs,
        "attestations": c.Attestations,
        "created_at": c.Attestations[0].Timestamp.Format(time.RFC3339),
        "last_updated": c.Attestations[0].Timestamp.Format(time.RFC3339),
    }

    if err := stateManager.SetRegion(ctx, mu, c.RegionID, region); err != nil {
        return nil, err
    }

    return &CreateRegionResult{
        RegionID:  c.RegionID,
        Success:   true,
        StateHash: c.Attestations[0].Data,
        Timestamp: c.Attestations[0].Timestamp.Format(time.RFC3339),
    }, nil
}

func (*CreateRegionAction) ComputeUnits(chain.Rules) uint64 {
    return 1
}

func (*CreateRegionAction) ValidRange(chain.Rules) (int64, int64) {
    return -1, -1
}

func (*UpdateRegionAction) GetTypeID() uint8 {
    return consts.UpdateRegionID
}

func (u *UpdateRegionAction) StateKeys(actor codec.Address) state.Keys {
    return state.Keys{
        string([]byte("region:" + u.RegionID)): state.Read | state.Write,
    }
}

func (u *UpdateRegionAction) Execute(
    ctx context.Context,
    _ chain.Rules,
    mu state.Mutable,
    _ int64,
    actor codec.Address,
    _ ids.ID,
) (codec.Typed, error) {
    stateManager := mu.(vm.StateManager)

    if len(u.RegionID) == 0 || len(u.RegionID) > 256 {
        return nil, ErrInvalidRegionID
    }

    if len(u.AddTEEs) > MaxTEEsPerRegion || len(u.RemTEEs) > MaxTEEsPerRegion {
        return nil, ErrTooManyTEEs
    }

    for _, tee := range u.AddTEEs {
        if len(tee) == 0 || len(tee) > 64 {
            return nil, ErrInvalidTEE
        }
    }
    for _, tee := range u.RemTEEs {
        if len(tee) == 0 || len(tee) > 64 {
            return nil, ErrInvalidTEE
        }
    }

    if err := verifyAttestationPair(u.Attestations); err != nil {
        return nil, err
    }

    region, err := stateManager.GetRegion(ctx, mu, u.RegionID)
    if err != nil {
        return nil, err
    }
    if region == nil {
        return nil, ErrRegionNotFound
    }

    currentTEEs, ok := region["tees"].([]types.TEEAddress)
    if !ok {
        return nil, errors.New("invalid region state format")
    }

    for _, remTEE := range u.RemTEEs {
        for i, tee := range currentTEEs {
            if bytes.Equal(tee, remTEE) {
                currentTEEs = append(currentTEEs[:i], currentTEEs[i+1:]...)
                break
            }
        }
    }

    currentTEEs = append(currentTEEs, u.AddTEEs...)

    if len(currentTEEs) > MaxTEEsPerRegion {
        return nil, ErrTooManyTEEs
    }

    region["tees"] = currentTEEs
    region["attestations"] = u.Attestations
    region["last_updated"] = u.Attestations[0].Timestamp.Format(time.RFC3339)

    if err := stateManager.SetRegion(ctx, mu, u.RegionID, region); err != nil {
        return nil, err
    }

    return &UpdateRegionResult{
        RegionID:  u.RegionID,
        Success:   true,
        StateHash: u.Attestations[0].Data,
        Timestamp: u.Attestations[0].Timestamp.Format(time.RFC3339),
    }, nil
}

func (*UpdateRegionAction) ComputeUnits(chain.Rules) uint64 {
    return 1
}

func (*UpdateRegionAction) ValidRange(chain.Rules) (int64, int64) {
    return -1, -1
}

func (*CreateRegionResult) GetTypeID() uint8 {
    return consts.CreateRegionResultID
}

func (*UpdateRegionResult) GetTypeID() uint8 {
    return consts.UpdateRegionResultID
}