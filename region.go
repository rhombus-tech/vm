// vm/region.go
package vm

import (
    "time"
    "github.com/rhombus-tech/vm/core"
    "github.com/rhombus-tech/vm/interfaces"
)

// Region represents a TEE execution region
type Region struct {
    ID            string                      `json:"id"`
    TEEPairs      []interfaces.TEEPairConfig  `json:"tee_pairs"`  // Updated to use TEEPairConfig
    Status        RegionStatus                `json:"status"`      // Use enum type
    CreatedAt     time.Time                   `json:"created_at"`
    LastUpdated   time.Time                   `json:"last_updated"`
    Attestations  [2]core.TEEAttestation      `json:"attestations"`
    MaxObjects    int                         `json:"max_objects"`
    MaxEvents     int                         `json:"max_events"`
}

// Region status type
type RegionStatus string

const (
    RegionStatusActive   RegionStatus = "active"
    RegionStatusInactive RegionStatus = "inactive"
    RegionStatusError    RegionStatus = "error"
)

// Validate checks if the region configuration is valid
func (r *Region) Validate() error {
    if r.ID == "" {
        return ErrInvalidRegionID
    }
    
    if len(r.TEEPairs) == 0 {
        return ErrInvalidTEE
    }
    
    if r.Status == "" {
        return ErrInvalidRegion
    }
    
    if r.CreatedAt.IsZero() {
        return ErrInvalidRegion
    }
    
    return nil
}

// IsActive checks if the region is in active status
func (r *Region) IsActive() bool {
    return r.Status == RegionStatusActive
}

// CanExecute checks if the region can execute new tasks
func (r *Region) CanExecute() bool {
    return r.IsActive() && len(r.TEEPairs) >= 1
}

// GetTEEPair returns the primary TEE pair for execution
func (r *Region) GetTEEPair() (*interfaces.TEEPairConfig, error) {
    if len(r.TEEPairs) == 0 {
        return nil, ErrInvalidTEE
    }
    
    return &r.TEEPairs[0], nil
}

// UpdateStatus updates the region status and last updated timestamp
func (r *Region) UpdateStatus(status RegionStatus) {  // Updated to use RegionStatus type
    r.Status = status
    r.LastUpdated = time.Now().UTC()
}

// AddTEEPair adds a new TEE pair to the region if not already present
func (r *Region) AddTEEPair(pair interfaces.TEEPairConfig) {
    // Check if pair already exists
    for _, existing := range r.TEEPairs {
        if existing.SGXEndpoint == pair.SGXEndpoint && 
           existing.SEVEndpoint == pair.SEVEndpoint {
            return
        }
    }
    r.TEEPairs = append(r.TEEPairs, pair)
}

// RemoveTEEPair removes a TEE pair from the region
func (r *Region) RemoveTEEPair(pair interfaces.TEEPairConfig) {
    newPairs := make([]interfaces.TEEPairConfig, 0, len(r.TEEPairs))
    for _, existing := range r.TEEPairs {
        if existing.SGXEndpoint != pair.SGXEndpoint || 
           existing.SEVEndpoint != pair.SEVEndpoint {
            newPairs = append(newPairs, existing)
        }
    }
    r.TEEPairs = newPairs
}

// Add helper methods for working with TEE pairs
func (r *Region) GetTEEPairByID(id string) (*interfaces.TEEPairConfig, error) {
    for _, pair := range r.TEEPairs {
        if pair.ID == id {
            return &pair, nil
        }
    }
    return nil, ErrInvalidTEE
}

func (r *Region) HasTEEPair(id string) bool {
    for _, pair := range r.TEEPairs {
        if pair.ID == id {
            return true
        }
    }
    return false
}

func (r *Region) UpdateTEEPair(pair interfaces.TEEPairConfig) error {
    for i, existing := range r.TEEPairs {
        if existing.ID == pair.ID {
            r.TEEPairs[i] = pair
            r.LastUpdated = time.Now().UTC()
            return nil
        }
    }
    return ErrInvalidTEE
}