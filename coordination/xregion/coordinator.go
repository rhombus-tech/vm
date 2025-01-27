package xregion

import (
	"context"
	"fmt"
	"sync"

	"github.com/rhombus-tech/vm/coordination/state"
)

var (
	globalCoordinator *Coordinator
	initOnce         sync.Once
)

// Coordinator handles cross-region state synchronization
type Coordinator struct {
	regionID  string
	state     *state.MerkleStore
	signer    interface{} // TODO: Use proper signer type
	transport interface{} // TODO: Use proper transport type
	mu        sync.RWMutex
}

// GetCoordinator returns the global coordinator instance
func GetCoordinator() *Coordinator {
	initOnce.Do(func() {
		store, err := state.NewMerkleStore(nil)
		if err != nil {
			// For now, just panic on initialization error
			panic(fmt.Sprintf("failed to initialize coordinator: %v", err))
		}
		// TODO: Initialize with proper configuration
		globalCoordinator = &Coordinator{
			regionID: "default",
			state:    store,
		}
	})
	return globalCoordinator
}

// NewCoordinator creates a new coordinator instance
func NewCoordinator(regionID string, state *state.MerkleStore, signer, transport interface{}) *Coordinator {
	return &Coordinator{
		regionID:  regionID,
		state:     state,
		signer:    signer,
		transport: transport,
	}
}

// RequestRangeProof requests a range proof from another region
func (c *Coordinator) RequestRangeProof(ctx context.Context, req *RangeRequest) (*RangeResponse, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Get range proof from state
	proof, err := c.state.GetRangeProof(ctx, req.StartKey, req.EndKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get range proof: %w", err)
	}

	// Create response
	resp := &RangeResponse{
		Proof:      proof,
		RegionID:   req.RegionID,
		TimeWindow: req.TimeWindow,
	}

	return resp, nil
}

// HandleRangeRequest processes a range proof request from another region
func (c *Coordinator) HandleRangeRequest(ctx context.Context, req *RangeRequest) (*RangeResponse, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Verify the request signature
	if err := c.verifyRequestSignature(req); err != nil {
		return nil, fmt.Errorf("invalid request signature: %w", err)
	}

	// Get range proof from state
	proof, err := c.state.GetRangeProof(ctx, req.StartKey, req.EndKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get range proof: %w", err)
	}

	// Create response
	resp := &RangeResponse{
		Proof:      proof,
		RegionID:   c.regionID,
		TimeWindow: req.TimeWindow,
	}

	// Sign response
	sig, err := c.signResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to sign response: %w", err)
	}
	resp.Signature = sig

	return resp, nil
}

func (c *Coordinator) verifyRequestSignature(req *RangeRequest) error {
	// TODO: Implement signature verification
	return nil
}

func (c *Coordinator) signResponse(resp *RangeResponse) ([]byte, error) {
	// TODO: Implement response signing
	return []byte{}, nil
}
