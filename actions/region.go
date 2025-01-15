// actions/region.go
package actions

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "time"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/avalanchego/x/merkledb"
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
    ErrStoreAccess        = errors.New("failed to access regional store")
    ErrProofGeneration    = errors.New("failed to generate merkle proof")
    ErrNotFound           = errors.New("not found")

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
    RegionID  string           `serialize:"true" json:"region_id"`
    Success   bool             `serialize:"true" json:"success"`
    StateHash []byte           `serialize:"true" json:"state_hash"`
    Timestamp string           `serialize:"true" json:"timestamp"`
    Proof     *merkledb.Proof  `serialize:"true" json:"proof"`
}

type UpdateRegionResult struct {
    RegionID  string           `serialize:"true" json:"region_id"`
    Success   bool             `serialize:"true" json:"success"`
    StateHash []byte           `serialize:"true" json:"state_hash"`
    Timestamp string           `serialize:"true" json:"timestamp"`
    Proof     *merkledb.Proof  `serialize:"true" json:"proof"`
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
    stateManager, ok := mu.(vm.StateManager)
    if !ok {
        return nil, errors.New("invalid state manager type")
    }

    if len(c.RegionID) == 0 || len(c.RegionID) > 256 {
        return nil, ErrInvalidRegionID
    }

    if len(c.TEEs) == 0 || len(c.TEEs) > MaxTEEsPerRegion {
        return nil, ErrTooManyTEEs
    }

    // Get regional store
    store, err := stateManager.GetRegionalStore(c.RegionID)
    if err != nil {
        return nil, fmt.Errorf("%w: %v", ErrStoreAccess, err)
    }

    // Check if region already exists
    key := makeRegionKey("config", c.RegionID, "")
    existing, err := store.Get(ctx, key)
    if err != nil && !errors.Is(err, ErrNotFound) {
    return nil, fmt.Errorf("failed to check existing region: %w", err)
}
    if existing != nil {
        return nil, ErrRegionExists
    }

    // Validate TEEs
    for _, tee := range c.TEEs {
        if len(tee) == 0 || len(tee) > 64 {
            return nil, ErrInvalidTEE
        }
    }

    if err := verifyAttestationPair(c.Attestations); err != nil {
        return nil, err
    }

    // Create region data
    regionData := map[string]interface{}{
        "tees": c.TEEs,
        "attestations": c.Attestations,
        "created_at": c.Attestations[0].Timestamp.Format(time.RFC3339),
        "last_updated": c.Attestations[0].Timestamp.Format(time.RFC3339),
    }

    // Marshal region data
    regionBytes, err := json.Marshal(regionData)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal region: %w", err)
    }

    // Store using regional key
    if err := store.Insert(ctx, key, regionBytes); err != nil {
        return nil, fmt.Errorf("failed to store region: %w", err)
    }

    // Get proof
    proof, err := store.GetProof(ctx, key)
    if err != nil {
        return nil, fmt.Errorf("%w: %v", ErrProofGeneration, err)
    }

    return &CreateRegionResult{
        RegionID:  c.RegionID,
        Success:   true,
        StateHash: c.Attestations[0].Data,
        Timestamp: c.Attestations[0].Timestamp.Format(time.RFC3339),
        Proof:     proof,
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
    stateManager, ok := mu.(vm.StateManager)
    if !ok {
        return nil, errors.New("invalid state manager type")
    }

    if len(u.RegionID) == 0 || len(u.RegionID) > 256 {
        return nil, ErrInvalidRegionID
    }

    // Get regional store
    store, err := stateManager.GetRegionalStore(u.RegionID)
    if err != nil {
        return nil, fmt.Errorf("%w: %v", ErrStoreAccess, err)
    }

    // Validate attestations
    if err := verifyAttestationPair(u.Attestations); err != nil {
        return nil, err
    }

    // Get existing region data
    key := makeRegionKey("config", u.RegionID, "")
    existingData, err := store.Get(ctx, key)
    if err != nil {
    if errors.Is(err, ErrNotFound) {
        return nil, ErrRegionNotFound
    }
    return nil, fmt.Errorf("failed to get existing region: %w", err)
}

    var region map[string]interface{}
    if err := json.Unmarshal(existingData, &region); err != nil {
        return nil, fmt.Errorf("failed to unmarshal region: %w", err)
    }

    // Update region data
    if u.SGXEndpoint != "" {
        region["sgx_endpoint"] = u.SGXEndpoint
    }
    if u.SEVEndpoint != "" {
        region["sev_endpoint"] = u.SEVEndpoint
    }
    region["attestations"] = u.Attestations
    region["last_updated"] = time.Now().UTC().Format(time.RFC3339)

    // Marshal updated data
    updatedData, err := json.Marshal(region)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal updated region: %w", err)
    }

    // Store update
    if err := store.Insert(ctx, key, updatedData); err != nil {
        return nil, fmt.Errorf("failed to store region update: %w", err)
    }

    // Get proof
    proof, err := store.GetProof(ctx, key)
    if err != nil {
        return nil, fmt.Errorf("%w: %v", ErrProofGeneration, err)
    }

    return &UpdateRegionResult{
        RegionID:  u.RegionID,
        Success:   true,
        StateHash: u.Attestations[0].Data,
        Timestamp: time.Now().UTC().Format(time.RFC3339),
        Proof:     proof,
    }, nil
}

func (*UpdateRegionAction) ComputeUnits(chain.Rules) uint64 {
    return 1
}

func (*UpdateRegionAction) ValidRange(chain.Rules) (int64, int64) {
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

func UnmarshalUpdateRegion(data []byte) (chain.Action, error) {
    p := codec.NewReader(data, len(data))
    u := &UpdateRegionAction{}
    u.Unmarshal(p)
    if err := p.Err(); err != nil {
        return nil, err
    }
    return u, nil
}

func (r *CreateRegionAction) Unmarshal(p *codec.Packer) {
    r.RegionID = p.UnpackString(false)

    addCount := p.UnpackInt(false)
    localTEEs := make([][]byte, addCount)
    for i := uint32(0); i < addCount; i++ {
        var tee []byte
        p.UnpackBytes(32, true, &tee)
        localTEEs[i] = tee
    }

    r.Attestations[0].Unmarshal(p)
    r.Attestations[1].Unmarshal(p)
}

func (u *UpdateRegionAction) Unmarshal(p *codec.Packer) {
    u.RegionID = p.UnpackString(false)
    u.SGXEndpoint = p.UnpackString(false)
    u.SEVEndpoint = p.UnpackString(false)
    u.Attestations[0].Unmarshal(p)
    u.Attestations[1].Unmarshal(p)
}

func (*CreateRegionResult) GetTypeID() uint8 {
    return consts.CreateRegionResultID
}

func (*UpdateRegionResult) GetTypeID() uint8 {
    return consts.UpdateRegionResultID
}

// Helper functions
func makeRegionKey(prefix, regionID, suffix string) []byte {
    if suffix == "" {
        return []byte(fmt.Sprintf("%s/%s", prefix, regionID))
    }
    return []byte(fmt.Sprintf("%s/%s/%s", prefix, regionID, suffix))
}

