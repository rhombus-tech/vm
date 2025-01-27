package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/state"
	"github.com/rhombus-tech/vm/coordination/xregion"
)

var (
	ErrNilIntent           = errors.New("nil cross-region intent")
	ErrInvalidTimeWindow   = errors.New("invalid time window")
	ErrEmptyStateChanges   = errors.New("empty state changes")
	ErrInvalidStateChanges = errors.New("invalid state changes")
)

type CrossRegionAction struct {
	Intent *xregion.CrossRegionIntent `json:"intent"`
}

func (a *CrossRegionAction) Execute(ctx context.Context, r chain.Rules, mu state.Mutable) (*chain.Result, error) {
	// Get required range proofs
	ranges := a.getRequiredRanges()
	proofs := make(map[string]*xregion.RangeResponse)

	// Request proofs for all required ranges
	for _, rng := range ranges {
		resp, err := xregion.GetCoordinator().RequestRangeProof(ctx, &rng)
		if err != nil {
			return nil, fmt.Errorf("failed to get range proof: %w", err)
		}

		proofs[rng.RegionID] = resp
	}

	// Verify all state changes are covered by proofs
	for regionID, changes := range a.Intent.StateChanges {
		proof, exists := proofs[regionID]
		if !exists {
			return nil, fmt.Errorf("missing proof for region %s", regionID)
		}

		// Verify each state change is in the proof
		for _, change := range changes {
			if _, exists := proof.Proof.Entries[string(change.Key)]; !exists {
				return nil, fmt.Errorf("state change key %x not in proof for region %s", change.Key, regionID)
			}
		}
	}

	// Apply state changes
	for _, changes := range a.Intent.StateChanges {
		for _, change := range changes {
			switch change.Operation {
			case xregion.StateOpSet:
				if err := mu.Insert(ctx, change.Key, change.Value); err != nil {
					return nil, fmt.Errorf("failed to apply state change: %w", err)
				}
			case xregion.StateOpDelete:
				if err := mu.Remove(ctx, change.Key); err != nil {
					return nil, fmt.Errorf("failed to apply state change: %w", err)
				}
			default:
				return nil, fmt.Errorf("unsupported operation: %v", change.Operation)
			}
		}
	}

	return &chain.Result{Success: true}, nil
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

	// Marshal status
	p.PackInt(uint32(a.Intent.Status))

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

	// Unmarshal status
	status := p.UnpackInt(true)
	a.Intent.Status = xregion.IntentStatus(status)

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

	return nil
}

func (a *CrossRegionAction) GetTypeID() uint8 {
	return 0x01 // Unique type ID for cross-region actions
}

func (a *CrossRegionAction) ComputeUnits(r chain.Rules) uint64 {
	// Base cost
	units := uint64(1000)

	// Add cost per state change
	for _, changes := range a.Intent.StateChanges {
		units += uint64(len(changes)) * 100
	}

	// Add cost per target region
	units += uint64(len(a.Intent.TargetRegions)) * 500

	return units
}

func (a *CrossRegionAction) ValidRange(r chain.Rules) (int64, int64) {
	return a.Intent.TimeWindow.Start.Unix(), a.Intent.TimeWindow.End().Unix()
}

func (a *CrossRegionAction) StateKeys(actor codec.Address) state.Keys {
    keys := make(state.Keys)
    for _, changes := range a.Intent.StateChanges {
        for _, change := range changes {
            keys[string(change.Key)] = state.Permissions(state.All) // Convert to Permissions type
        }
    }
    return keys
}

// getRequiredRanges determines which ranges need to be requested for state verification
func (a *CrossRegionAction) getRequiredRanges() []xregion.RangeRequest {
	// Group changes by region
	rangesByRegion := make(map[string]xregion.RangeRequest)
	for regionID, changes := range a.Intent.StateChanges {
		if len(changes) == 0 {
			continue
		}

		// Sort changes by key to find range boundaries
		sort.Slice(changes, func(i, j int) bool {
			return bytes.Compare(changes[i].Key, changes[j].Key) < 0
		})

		// Create range request
		rangesByRegion[regionID] = xregion.RangeRequest{
			RegionID:   regionID,
			StartKey:   changes[0].Key,
			EndKey:     changes[len(changes)-1].Key,
			TimeWindow: xregion.TimeWindow{
				Duration: 60 * time.Second, // TODO: Make configurable
			},
		}
	}

	// Convert map to slice
	ranges := make([]xregion.RangeRequest, 0, len(rangesByRegion))
	for _, rng := range rangesByRegion {
		ranges = append(ranges, rng)
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

	// Group changes with similar prefixes
	var ranges []struct{ Start, End []byte }
	start := changes[0].Key
	prev := changes[0].Key

	for i := 1; i < len(changes); i++ {
		curr := changes[i].Key
		// If keys are too far apart, start a new range
		if !bytes.HasPrefix(curr, prev[:len(prev)/2]) {
			ranges = append(ranges, struct{ Start, End []byte }{start, prev})
			start = curr
		}
		prev = curr
	}
	ranges = append(ranges, struct{ Start, End []byte }{start, prev})

	return ranges
}
