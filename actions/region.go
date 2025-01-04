package actions

import (
	"bytes"
	"context"
	"errors"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/state"
	
	"github.com/rhombus-tech/vm/storage"
	mconsts "github.com/rhombus-tech/vm/consts"
)

var (
	ErrRegionExists      = errors.New("region already exists")
	ErrRegionNotFound    = errors.New("region not found")
	ErrInvalidTEE       = errors.New("invalid TEE")
	ErrInvalidRegionID  = errors.New("invalid region ID")
	ErrTooManyTEEs      = errors.New("too many TEEs")
	ErrMissingAttestation = errors.New("missing TEE attestation")
	ErrAttestationMismatch = errors.New("attestation pair mismatch")
	
	_ chain.Action = (*CreateRegionAction)(nil)
	_ chain.Action = (*UpdateRegionAction)(nil)

	MaxTEEsPerRegion = 32
)

type TEEAddress []byte

type TEEAttestation struct {
	EnclaveID   []byte `serialize:"true" json:"enclave_id"`
	Measurement []byte `serialize:"true" json:"measurement"`
	Timestamp   string `serialize:"true" json:"timestamp"`
	Data        []byte `serialize:"true" json:"data"`
	Signature   []byte `serialize:"true" json:"signature"`
	RegionProof []byte `serialize:"true" json:"region_proof"`
}

type CreateRegionAction struct {
	RegionID     string          `serialize:"true" json:"region_id"`
	TEEs         []TEEAddress    `serialize:"true" json:"tees"`
	Attestations [2]TEEAttestation `serialize:"true" json:"attestations"`
}

func (*CreateRegionAction) GetTypeID() uint8 {
	return mconsts.CreateRegionID
}

func (c *CreateRegionAction) StateKeys(actor codec.Address, _ ids.ID) state.Keys {
	return state.Keys{
		string(storage.RegionKey(c.RegionID)): state.Write,
	}
}

func verifyAttestationPair(attestations [2]TEEAttestation) error {
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

func (c *CreateRegionAction) Execute(
	ctx context.Context,
	_ chain.Rules,
	mu state.Mutable,
	_ int64,
	actor codec.Address,
	_ ids.ID,
) (codec.Typed, error) {
	// Validate region ID
	if len(c.RegionID) == 0 || len(c.RegionID) > 256 {
		return nil, ErrInvalidRegionID
	}

	// Validate TEE list
	if len(c.TEEs) == 0 || len(c.TEEs) > MaxTEEsPerRegion {
		return nil, ErrTooManyTEEs
	}

	// Validate individual TEEs
	for _, tee := range c.TEEs {
		if len(tee) == 0 || len(tee) > 64 { // Assuming reasonable TEE ID size
			return nil, ErrInvalidTEE
		}
	}

	// Validate attestations
	if err := verifyAttestationPair(c.Attestations); err != nil {
		return nil, err
	}

	// Check if region already exists
	exists, err := storage.RegionExists(ctx, mu, c.RegionID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrRegionExists
	}

	// Create region state
	region := map[string]interface{}{
		"tees": c.TEEs,
		"attestations": c.Attestations,
		"created_at": c.Attestations[0].Timestamp,
		"last_updated": c.Attestations[0].Timestamp,
	}

	if err := storage.SetRegion(ctx, mu, c.RegionID, region); err != nil {
		return nil, err
	}

	return &CreateRegionResult{
		RegionID:  c.RegionID,
		Success:   true,
		StateHash: c.Attestations[0].Data,
		Timestamp: c.Attestations[0].Timestamp,
	}, nil
}

func (*CreateRegionAction) ComputeUnits(chain.Rules) uint64 {
	return 1
}

func (*CreateRegionAction) ValidRange(chain.Rules) (int64, int64) {
	return -1, -1
}

type UpdateRegionAction struct {
	RegionID     string          `serialize:"true" json:"region_id"`
	AddTEEs      []TEEAddress    `serialize:"true" json:"add_tees"`
	RemTEEs      []TEEAddress    `serialize:"true" json:"rem_tees"`
	Attestations [2]TEEAttestation `serialize:"true" json:"attestations"`
}

func (*UpdateRegionAction) GetTypeID() uint8 {
	return mconsts.UpdateRegionID
}

func (u *UpdateRegionAction) StateKeys(actor codec.Address, _ ids.ID) state.Keys {
	return state.Keys{
		string(storage.RegionKey(u.RegionID)): state.Read | state.Write,
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
	// Validate region ID
	if len(u.RegionID) == 0 || len(u.RegionID) > 256 {
		return nil, ErrInvalidRegionID
	}

	// Validate TEE lists
	if len(u.AddTEEs) > MaxTEEsPerRegion || len(u.RemTEEs) > MaxTEEsPerRegion {
		return nil, ErrTooManyTEEs
	}

	// Validate individual TEEs
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

	// Validate attestations
	if err := verifyAttestationPair(u.Attestations); err != nil {
		return nil, err
	}

	// Get existing region
	region, err := storage.GetRegion(ctx, mu, u.RegionID)
	if err != nil {
		return nil, err
	}
	if region == nil {
		return nil, ErrRegionNotFound
	}

	currentTEEs, ok := region["tees"].([]TEEAddress)
	if !ok {
		return nil, errors.New("invalid region state format")
	}

	// Remove TEEs
	for _, remTEE := range u.RemTEEs {
		for i, tee := range currentTEEs {
			if bytes.Equal(tee, remTEE) {
				currentTEEs = append(currentTEEs[:i], currentTEEs[i+1:]...)
				break
			}
		}
	}

	// Add new TEEs
	currentTEEs = append(currentTEEs, u.AddTEEs...)

	// Validate final TEE count
	if len(currentTEEs) > MaxTEEsPerRegion {
		return nil, ErrTooManyTEEs
	}

	// Update region state
	region["tees"] = currentTEEs
	region["attestations"] = u.Attestations
	region["last_updated"] = u.Attestations[0].Timestamp

	if err := storage.SetRegion(ctx, mu, u.RegionID, region); err != nil {
		return nil, err
	}

	return &UpdateRegionResult{
		RegionID:  u.RegionID,
		Success:   true,
		StateHash: u.Attestations[0].Data,
		Timestamp: u.Attestations[0].Timestamp,
	}, nil
}

func (*UpdateRegionAction) ComputeUnits(chain.Rules) uint64 {
	return 1
}

func (*UpdateRegionAction) ValidRange(chain.Rules) (int64, int64) {
	return -1, -1
}

type CreateRegionResult struct {
	RegionID  string `serialize:"true" json:"region_id"`
	Success   bool   `serialize:"true" json:"success"`
	StateHash []byte `serialize:"true" json:"state_hash"`
	Timestamp string `serialize:"true" json:"timestamp"`
}

func (*CreateRegionResult) GetTypeID() uint8 {
	return mconsts.CreateRegionResultID
}

type UpdateRegionResult struct {
	RegionID  string `serialize:"true" json:"region_id"`
	Success   bool   `serialize:"true" json:"success"`
	StateHash []byte `serialize:"true" json:"state_hash"`
	Timestamp string `serialize:"true" json:"timestamp"`
}

func (*UpdateRegionResult) GetTypeID() uint8 {
	return mconsts.UpdateRegionResultID
}