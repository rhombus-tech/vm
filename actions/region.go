package actions

import (
    "context"
    "errors"
    "time"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
    
    "github.com/rhombus-tech/vm"       
    "github.com/rhombus-tech/vm/core" 
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
    TEEs         []core.TEEAddress      `serialize:"true" json:"tees"`
    Attestations [2]core.TEEAttestation `serialize:"true" json:"attestations"`
}

type UpdateRegionAction struct {
    RegionID     string                  `serialize:"true" json:"region_id"`
    SGXEndpoint  string                  `serialize:"true" json:"sgx_endpoint"`
    SEVEndpoint  string                  `serialize:"true" json:"sev_endpoint"`
    Attestations [2]core.TEEAttestation  `serialize:"true" json:"attestations"`
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

func UnmarshalCreateRegion(data []byte) (chain.Action, error) {
    p := codec.NewReader(data, len(data))
    r := &CreateRegionAction{}
    r.Unmarshal(p)
    if err := p.Err(); err != nil {
        return nil, err
    }
    return r, nil
}

func (r *CreateRegionAction) Unmarshal(p *codec.Packer) {
    // Suppose "RegionID" is the only field we actually want to store in the struct
    r.RegionID = p.UnpackString(false)

    // 1) read the count of TEE addresses
    addCount := p.UnpackInt(false)

    // 2) create a local slice (not on the struct)
    localTEEs := make([][]byte, addCount)
    for i := uint32(0); i < addCount; i++ {
        var tee []byte
        p.UnpackBytes(32, true, &tee)
        localTEEs[i] = tee
    }

    // If you only need these TEE addresses temporarily or you want
    // to process them in some immediate way, do it now:
    // e.g. check them, pass them along, etc.

    // 3) read your attestation objects
    r.Attestations[0].Unmarshal(p)
    r.Attestations[1].Unmarshal(p)


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
    // 1) Convert the generic state.Mutable into your custom StateManager
    stateManager, ok := mu.(vm.StateManager)
    if !ok {
        return nil, errors.New("invalid state manager type")
    }

    // 2) Validate region ID
    if len(u.RegionID) == 0 || len(u.RegionID) > 256 {
        return nil, errors.New("invalid region ID")
    }

    // 3) Validate the attestation pair if needed
    if err := verifyAttestationPair(u.Attestations); err != nil {
        return nil, err
    }

    // 4) Load existing region from state
    region, err := stateManager.GetRegion(ctx, mu, u.RegionID)
    if err != nil {
        return nil, err
    }
    if region == nil {
        return nil, errors.New("region not found")
    }

    // 5) Update endpoints if provided
    if u.SGXEndpoint != "" {
        region["sgx_endpoint"] = u.SGXEndpoint
    }
    if u.SEVEndpoint != "" {
        region["sev_endpoint"] = u.SEVEndpoint
    }

    // 6) Update attestation data
    region["attestations"] = u.Attestations
    region["last_updated"] = time.Now().UTC().Format(time.RFC3339)

    // 7) Save updated region map
    if err := stateManager.SetRegion(ctx, mu, u.RegionID, region); err != nil {
        return nil, err
    }

    // 8) Return a typed result
    return &UpdateRegionResult{
        RegionID:  u.RegionID,
        Success:   true,
        StateHash: u.Attestations[0].Data,
        Timestamp: time.Now().UTC().Format(time.RFC3339),
    }, nil
}

func UnmarshalUpdateRegion(data []byte) (chain.Action, error) {
    p := codec.NewReader(data, len(data))
    u := &UpdateRegionAction{}
    u.Unmarshal(p)
    if err := p.Err(); err != nil {
        return nil, err
    }
    return u, nil
}

func (u *UpdateRegionAction) Unmarshal(p *codec.Packer) {
    // 1) read RegionID
    u.RegionID = p.UnpackString(false)

    // 2) read [addCount], but store TEEs in a local var
    addCount := p.UnpackInt(false)
    localAdd := make([][]byte, addCount)
    for i := uint32(0); i < addCount; i++ {
        var tee []byte
        p.UnpackBytes(32, true, &tee)
        localAdd[i] = tee
    }
    // ... do something ephemeral with localAdd or ignore it

    // 3) read [remCount], also local
    remCount := p.UnpackInt(false)
    localRem := make([][]byte, remCount)
    for i := uint32(0); i < remCount; i++ {
        var tee []byte
        p.UnpackBytes(32, true, &tee)
        localRem[i] = tee
    }
    // ... do something ephemeral with localRem

    // 4) read Attestations array
    u.Attestations[0].Unmarshal(p)
    u.Attestations[1].Unmarshal(p)
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


