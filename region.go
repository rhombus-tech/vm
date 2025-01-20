// vm/region.go
package vm

import (
    "time"
    "github.com/rhombus-tech/vm/core"
)

// Region represents a TEE execution region
type Region struct {
    ID            string                `json:"id"`
    TEEPairs      []TEEPair            `json:"tee_pairs"`
    Status        string               `json:"status"`
    CreatedAt     time.Time           `json:"created_at"`
    LastUpdated   time.Time           `json:"last_updated"`
    Attestations  [2]core.TEEAttestation `json:"attestations"`
    MaxObjects    int                  `json:"max_objects"`
    MaxEvents     int                  `json:"max_events"`
}

type TEEPair struct {
    ID          string `json:"id"`
    SGXEndpoint string `json:"sgx_endpoint"`
    SEVEndpoint string `json:"sev_endpoint"`
    Status      string `json:"status"`
}

const (
    RegionStatusActive   = "active"
    RegionStatusInactive = "inactive"
    RegionStatusError    = "error"
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
func (r *Region) GetTEEPair() (*TEEPair, error) {
    if len(r.TEEPairs) == 0 {
        return nil, ErrInvalidTEE
    }
    
    return &r.TEEPairs[0], nil
}

// UpdateStatus updates the region status and last updated timestamp
func (r *Region) UpdateStatus(status string) {
    r.Status = status
    r.LastUpdated = time.Now().UTC()
}

// AddTEEPair adds a new TEE pair to the region if not already present
func (r *Region) AddTEEPair(pair TEEPair) {
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
func (r *Region) RemoveTEEPair(pair TEEPair) {
    newPairs := make([]TEEPair, 0, len(r.TEEPairs))
    for _, existing := range r.TEEPairs {
        if existing.SGXEndpoint != pair.SGXEndpoint || 
           existing.SEVEndpoint != pair.SEVEndpoint {
            newPairs = append(newPairs, existing)
        }
    }
    r.TEEPairs = newPairs
}