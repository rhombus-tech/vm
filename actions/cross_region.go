package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/state"
	"github.com/rhombus-tech/vm/consts"
	"github.com/rhombus-tech/vm/coordination/xregion"
)

var (
	ErrNilIntent           = errors.New("nil cross-region intent")
	ErrInvalidTimeWindow   = errors.New("invalid time window")
	ErrEmptyStateChanges   = errors.New("empty state changes")
	ErrInvalidStateChanges = errors.New("invalid state changes")
	ErrInvalidSignature    = errors.New("invalid signature")
	ErrMissingSignature    = errors.New("missing required signature")
)

type CrossRegionAction struct {
	Intent *xregion.CrossRegionIntent `json:"intent"`
}

func (a *CrossRegionAction) Execute(
	ctx context.Context,
	_ chain.Rules,
	mu state.Mutable,
	_ int64,
	actor codec.Address,
	_ ids.ID,
) (codec.Typed, error) {
	// Validate the action
	if err := a.ValidateBasic(); err != nil {
		return nil, err
	}

	coordinator := xregion.GetCoordinator()
	
	// Create RegionProcessor with reasonable concurrency limit
	processor, err := xregion.NewRegionProcessor(4, coordinator)
	if err != nil {
		return nil, fmt.Errorf("failed to create region processor: %w", err)
	}

	// Get required range proofs
	ranges := a.getRequiredRanges()
	proofs := make(map[string]*xregion.RangeResponse)

	// Request proofs for all required ranges
	for _, rng := range ranges {
		resp, err := coordinator.RequestRangeProof(ctx, &rng)
		if err != nil {
			return nil, fmt.Errorf("failed to get range proof: %w", err)
		}

		proofs[rng.RegionID] = resp
	}

	// Get list of regions that need confirmation
	var regions []string
	for regionID := range a.Intent.StateChanges {
		if len(a.Intent.StateChanges[regionID]) > 0 {
			regions = append(regions, regionID)
		}
	}

	// Process regions in parallel
	signature, err := coordinator.Sign([]byte(a.Intent.ID))
	if err != nil {
		return nil, fmt.Errorf("failed to sign intent: %w", err)
	}

	if err := processor.ProcessRegions(ctx, a.Intent.ID, regions, signature); err != nil {
		return nil, fmt.Errorf("failed to process regions: %w", err)
	}

	// Verify all state changes are covered by proofs
	for regionID, changes := range a.Intent.StateChanges {
		if len(changes) == 0 {
			continue
		}
		
		proof, exists := proofs[regionID]
		if !exists {
			return nil, fmt.Errorf("missing proof for region %s", regionID)
		}

		// Verify each state change is in the proof
		for _, change := range changes {
			exists := false
			for key := range proof.Proof.Entries {
				if bytes.Equal([]byte(key), change.Key) {
					exists = true
					break
				}
			}
			if !exists {
				return nil, fmt.Errorf("state change not covered by proof for region %s", regionID)
			}
		}
	}

	// Apply state changes
	for _, changes := range a.Intent.StateChanges {
		if len(changes) == 0 {
			continue
		}
		
		for _, change := range changes {
			key := string(change.Key)
			switch change.Operation {
			case xregion.StateOpSet:
				if err := mu.Insert(ctx, []byte(key), change.Value); err != nil {
					return nil, fmt.Errorf("failed to set state: %w", err)
				}
			case xregion.StateOpTransferOut:
				if err := mu.Remove(ctx, []byte(key)); err != nil {
					return nil, fmt.Errorf("failed to remove state: %w", err)
				}
			}
		}
	}

	return &CrossRegionResult{Success: true}, nil
}

func (a *CrossRegionAction) ValidateBasic() error {
	if a.Intent == nil {
		return ErrNilIntent
	}

	// Check time window
	if a.Intent.TimeWindow.Duration <= 0 {
		return ErrInvalidTimeWindow
	}

	// Check state changes
	if len(a.Intent.StateChanges) == 0 {
		return ErrEmptyStateChanges
	}

	// Validate state changes
	if err := a.Intent.ValidateStateChanges(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidStateChanges, err)
	}

	// Verify required signatures
	if err := a.verifySignatures(); err != nil {
		return err
	}

	return nil
}

func (a *CrossRegionAction) verifySignatures() error {
	// Source region must sign
	if _, ok := a.Intent.Signatures[a.Intent.SourceRegion]; !ok {
		return fmt.Errorf("%w: missing source region signature", ErrMissingSignature)
	}

	// All target regions must sign
	for _, region := range a.Intent.TargetRegions {
		if _, ok := a.Intent.Signatures[region]; !ok {
			return fmt.Errorf("%w: missing target region signature", ErrMissingSignature)
		}
	}

	// Verify each signature
	for region, sig := range a.Intent.Signatures {
		if err := xregion.GetCoordinator().VerifySignature(region, a.Intent.ID, sig); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
		}
	}

	return nil
}

func (a *CrossRegionAction) Marshal(p *codec.Packer) error {
	if a.Intent == nil {
		return ErrNilIntent
	}

	// Marshal intent fields
	p.PackString(a.Intent.ID)
	p.PackString(a.Intent.SourceRegion)
	p.PackInt(uint32(len(a.Intent.TargetRegions)))
	for _, region := range a.Intent.TargetRegions {
		p.PackString(region)
	}

	// Marshal timestamps
	p.PackInt64(int64(a.Intent.TimeWindow.Duration))

	// Marshal state changes
	p.PackInt(uint32(len(a.Intent.StateChanges)))
	for region, changes := range a.Intent.StateChanges {
		p.PackString(region)
		p.PackInt(uint32(len(changes)))
		for _, change := range changes {
			p.PackBytes(change.Key)
			p.PackBytes(change.Value)
			p.PackInt(uint32(change.Operation))
			p.PackString(change.Source)
			p.PackString(change.Target)
		}
	}

	// Marshal signatures
	p.PackInt(uint32(len(a.Intent.Signatures)))
	for regionID, sig := range a.Intent.Signatures {
		p.PackString(regionID)
		p.PackBytes(sig)
	}

	return p.Err()
}

func (a *CrossRegionAction) Unmarshal(p *codec.Packer) error {
	a.Intent = &xregion.CrossRegionIntent{
		TargetRegions: make([]string, 0),
		StateChanges:  make(map[string][]xregion.StateChange),
		Signatures:    make(map[string][]byte),
	}

	// Unmarshal intent fields
	a.Intent.ID = p.UnpackString(true)
	a.Intent.SourceRegion = p.UnpackString(true)
	targetCount := p.UnpackInt(true)
	for i := uint32(0); i < targetCount; i++ {
		region := p.UnpackString(true)
		a.Intent.TargetRegions = append(a.Intent.TargetRegions, region)
	}

	// Unmarshal timestamps
	duration := p.UnpackInt64(true)
	a.Intent.TimeWindow.Duration = time.Duration(duration)
	a.Intent.TimeWindow.Start = time.Now() // Set current time as start

	// Unmarshal state changes
	numRegionChanges := p.UnpackInt(true)
	for i := uint32(0); i < numRegionChanges; i++ {
		regionID := p.UnpackString(true)
		numChanges := p.UnpackInt(true)
		changes := make([]xregion.StateChange, numChanges)
		for j := uint32(0); j < numChanges; j++ {
			var key, value []byte
			p.UnpackBytes(0, true, &key)
			p.UnpackBytes(0, true, &value)
			changes[j].Key = key
			changes[j].Value = value
			changes[j].Operation = xregion.StateOperation(p.UnpackInt(true))
			changes[j].Source = p.UnpackString(true)
			changes[j].Target = p.UnpackString(true)
		}
		a.Intent.StateChanges[regionID] = changes
	}

	// Unmarshal signatures
	numSigs := p.UnpackInt(true)
	for i := uint32(0); i < numSigs; i++ {
		regionID := p.UnpackString(true)
		var sig []byte
		p.UnpackBytes(0, true, &sig)
		a.Intent.Signatures[regionID] = sig
	}

	return p.Err()
}

func (a *CrossRegionAction) GetTypeID() uint8 {
	return consts.CrossRegionID
}

func (a *CrossRegionAction) ComputeUnits(r chain.Rules) uint64 {
	// Base cost plus additional cost per state change
	baseCost := uint64(1000)
	stateChangeCost := uint64(100)
	
	totalChanges := uint64(0)
	for _, changes := range a.Intent.StateChanges {
		totalChanges += uint64(len(changes))
	}
	
	return baseCost + (stateChangeCost * totalChanges)
}

func (a *CrossRegionAction) ValidRange(r chain.Rules) (int64, int64) {
	if a.Intent == nil {
		return time.Now().Unix(), time.Now().Add(5 * time.Minute).Unix()
	}
	return a.Intent.TimeWindow.Start.Unix(), a.Intent.TimeWindow.End().Unix()
}

func (a *CrossRegionAction) StateKeys(actor codec.Address) state.Keys {
	keys := make(state.Keys)
	
	// Add all state changes to the keys
	for _, changes := range a.Intent.StateChanges {
		for _, change := range changes {
			key := string(change.Key)
			switch change.Operation {
			case xregion.StateOpSet:
				keys[key] = state.Write
			case xregion.StateOpTransferOut:
				keys[key] = state.Write
			}
		}
	}
	
	return keys
}

// getRequiredRanges determines which ranges need to be requested for state verification
func (a *CrossRegionAction) getRequiredRanges() []xregion.RangeRequest {
	var ranges []xregion.RangeRequest

	// Group changes by region and key prefix
	for regionID, changes := range a.Intent.StateChanges {
		if len(changes) == 0 {
			continue
		}
		
		keyRanges := groupChangesByPrefix(changes)
		for _, r := range keyRanges {
			ranges = append(ranges, xregion.RangeRequest{
				StartKey:   r.Start,
				EndKey:     r.End,
				RegionID:   regionID,
				TimeWindow: xregion.TimeWindow{
					Start:    time.Now(),
					Duration: 5 * time.Minute,
				},
			})
		}
	}

	return ranges
}

// groupChangesByPrefix groups state changes by their key prefix for efficient range requests
func groupChangesByPrefix(changes []xregion.StateChange) []struct{ Start, End []byte } {
	if len(changes) == 0 {
		return nil
	}

	// Sort changes by key
	sort.Slice(changes, func(i, j int) bool {
		return bytes.Compare(changes[i].Key, changes[j].Key) < 0
	})

	var ranges []struct{ Start, End []byte }
	currentRange := struct{ Start, End []byte }{
		Start: changes[0].Key,
		End:   changes[0].Key,
	}

	for i := 1; i < len(changes); i++ {
		// If keys are contiguous, extend current range
		if bytes.Equal(changes[i].Key[:8], currentRange.End[:8]) {
			currentRange.End = changes[i].Key
			continue
		}

		// Start new range
		ranges = append(ranges, currentRange)
		currentRange = struct{ Start, End []byte }{
			Start: changes[i].Key,
			End:   changes[i].Key,
		}
	}

	ranges = append(ranges, currentRange)
	return ranges
}

type CrossRegionResult struct {
	Success bool `serialize:"true" json:"success"`
}

func (*CrossRegionResult) GetTypeID() uint8 {
	return consts.CrossRegionResultID
}
