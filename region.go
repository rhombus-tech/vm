// vm/region.go
package vm

import (
	"bytes"
    "time"
    "github.com/rhombus-tech/vm/core"
)

// Region represents a TEE execution region
type Region struct {
    ID            string                `json:"id"`
    TEEs          []core.TEEAddress    `json:"tees"`
    Status        string               `json:"status"`
    CreatedAt     time.Time           `json:"created_at"`
    LastUpdated   time.Time           `json:"last_updated"`
    Attestations  [2]core.TEEAttestation `json:"attestations"`
    MaxObjects    int                  `json:"max_objects"`
    MaxEvents     int                  `json:"max_events"`
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
    
    if len(r.TEEs) == 0 {
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
    return r.IsActive() && len(r.TEEs) >= 2
}

// GetTEEPair returns the primary TEE pair for execution
func (r *Region) GetTEEPair() ([2]core.TEEAddress, error) {
    if len(r.TEEs) < 2 {
        return [2]core.TEEAddress{}, ErrInvalidTEE
    }
    
    return [2]core.TEEAddress{r.TEEs[0], r.TEEs[1]}, nil
}

// UpdateStatus updates the region status and last updated timestamp
func (r *Region) UpdateStatus(status string) {
    r.Status = status
    r.LastUpdated = time.Now().UTC()
}

// AddTEE adds a new TEE to the region if not already present
func (r *Region) AddTEE(tee core.TEEAddress) {
    // Check if TEE already exists
    for _, existing := range r.TEEs {
        if bytes.Equal(existing, tee) {
            return
        }
    }
    r.TEEs = append(r.TEEs, tee)
}

// RemoveTEE removes a TEE from the region
func (r *Region) RemoveTEE(tee core.TEEAddress) {
    newTEEs := make([]core.TEEAddress, 0, len(r.TEEs))
    for _, existing := range r.TEEs {
        if !bytes.Equal(existing, tee) {
            newTEEs = append(newTEEs, existing)
        }
    }
    r.TEEs = newTEEs
}