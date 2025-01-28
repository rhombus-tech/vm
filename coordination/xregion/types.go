package xregion

import (
	"fmt"
	"time"

	"github.com/rhombus-tech/vm/coordination/state"
)

// Now returns the current time
func Now() time.Time {
	return time.Now().UTC()
}

// CrossRegionIntent represents a cross-region transaction intent
type CrossRegionIntent struct {
	ID            string
	SourceRegion  string
	TargetRegions []string
	TimeWindow    TimeWindow
	Status        IntentStatus
	CreatedAt     time.Time
	Signatures    map[string][]byte

	// State changes per region
	StateChanges map[string][]StateChange

	// Dependencies between regions
	Dependencies map[string][]string

	Data []byte
}

// TimeWindow represents the execution window for a transaction
type TimeWindow struct {
	Start    time.Time
	Duration time.Duration
}

// End returns the end time of the window
func (w TimeWindow) End() time.Time {
	return w.Start.Add(w.Duration)
}

// IntentStatus represents the status of a cross-region intent
type IntentStatus int

const (
	IntentStatusPending IntentStatus = iota
	IntentStatusConfirmed
	IntentStatusExecuting
	IntentStatusCommitted
	IntentStatusAborted
	IntentStatusFailed
)

// StateChange represents a change to be applied to a region's state
type StateChange struct {
	Key       []byte
	Value     []byte
	Operation StateOperation
	Source    string
	Target    string
}

// StateOperation represents the type of state change operation
type StateOperation int

const (
	StateOpSet StateOperation = iota
	StateOpDelete
	StateOpTransferOut  // Remove from source region
	StateOpTransferIn   // Add to target region
	StateOpLock        // Lock for cross-region operation
	StateOpUnlock      // Unlock after cross-region operation
)

// ExecutionProof represents proof of execution in a region
type ExecutionProof struct {
	IntentID       string
	RegionID       string
	Timestamp      time.Time
	StateHash      []byte
	PreviousProofs map[string]*ExecutionProof
}

// RangeRequest represents a request for a range proof from another region
type RangeRequest struct {
	StartKey   []byte     `json:"start_key"`
	EndKey     []byte     `json:"end_key"`
	RegionID   string     `json:"region_id"`
	TimeWindow TimeWindow `json:"time_window"`
	Signature  []byte     `json:"signature"`
}

// RangeResponse represents a response containing a range proof
type RangeResponse struct {
	Proof      *state.RangeProof `json:"proof"`
	RegionID   string            `json:"region_id"`
	TimeWindow TimeWindow        `json:"time_window"`
	Signature  []byte            `json:"signature"`
}

// NewCrossRegionIntent creates a new cross-region intent with proper initialization
func NewCrossRegionIntent(id, sourceRegion string, targetRegions []string) *CrossRegionIntent {
	return &CrossRegionIntent{
		ID:           id,
		SourceRegion: sourceRegion,
		TargetRegions: targetRegions,
		Status:       IntentStatusPending,
		CreatedAt:    time.Now(),
		Signatures:   make(map[string][]byte),
		StateChanges: make(map[string][]StateChange),
		Dependencies: make(map[string][]string),
	}
}

// AddStateChange adds a state change for a specific region
func (i *CrossRegionIntent) AddStateChange(regionID string, change StateChange) {
	if i.StateChanges == nil {
		i.StateChanges = make(map[string][]StateChange)
	}
	i.StateChanges[regionID] = append(i.StateChanges[regionID], change)
}

// AddDependency adds a dependency between regions
func (i *CrossRegionIntent) AddDependency(regionID, dependsOn string) {
	if i.Dependencies == nil {
		i.Dependencies = make(map[string][]string)
	}
	// Check if dependency already exists
	for _, dep := range i.Dependencies[regionID] {
		if dep == dependsOn {
			return
		}
	}
	i.Dependencies[regionID] = append(i.Dependencies[regionID], dependsOn)
}

// GetDependencies returns all regions that must execute before the given region
func (i *CrossRegionIntent) GetDependencies(regionID string) []string {
	return i.Dependencies[regionID]
}

// GetStateChanges returns all state changes for a specific region
func (i *CrossRegionIntent) GetStateChanges(regionID string) []StateChange {
	return i.StateChanges[regionID]
}

// ValidateStateChanges checks if state changes are consistent across regions
func (i *CrossRegionIntent) ValidateStateChanges() error {
	// Track transfers between regions
	type transfer struct {
		source string
		target string
		key    string
	}
	transfers := make(map[string]transfer)

	// Check each region's state changes
	for region, changes := range i.StateChanges {
		for _, change := range changes {
			key := string(change.Key)
			switch change.Operation {
			case StateOpTransferOut:
				if change.Target == "" {
					return fmt.Errorf("transfer out from %s missing target region", region)
				}
				transfers[key] = transfer{
					source: region,
					target: change.Target,
					key:    key,
				}
			case StateOpTransferIn:
				if change.Source == "" {
					return fmt.Errorf("transfer in to %s missing source region", region)
				}
				t, exists := transfers[key]
				if !exists {
					return fmt.Errorf("transfer in to %s has no matching transfer out", region)
				}
				if t.target != region {
					return fmt.Errorf("transfer target mismatch: expected %s, got %s", t.target, region)
				}
			}
		}
	}

	return nil
}
