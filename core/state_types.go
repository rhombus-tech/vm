// core/state_types.go
package core

import (
    "errors"
    "fmt"
    "time"
)

type ObjectState struct {
    Code        []byte    `serialize:"true" json:"code"`
    Storage     []byte    `serialize:"true" json:"storage"`
    RegionID    string    `serialize:"true" json:"region_id"`
    Events      []string  `serialize:"true" json:"events"`
    LastUpdated time.Time `serialize:"true" json:"last_updated"`
    Status      string    `serialize:"true" json:"status"`
}

type Event struct {
    FunctionCall string           `serialize:"true" json:"function_call"`
    Parameters   []byte           `serialize:"true" json:"parameters"`
    Attestations [2]TEEAttestation `serialize:"true" json:"attestations"`
    Timestamp    string           `serialize:"true" json:"timestamp"`
    Status       string           `serialize:"true" json:"status"`
}

// Constants
const (
    StatusActive   = "active"
    StatusPending  = "pending"
    StatusComplete = "complete"
    StatusError    = "error"

    MaxCodeSize    = 1024 * 1024    // 1MB
    MaxStorageSize = 1024 * 1024    // 1MB
)

// Object validation errors
var (
    ErrCodeTooLarge     = errors.New("code size exceeds maximum")
    ErrStorageTooLarge  = errors.New("storage size exceeds maximum")
    ErrInvalidRegionID  = errors.New("invalid region ID")
    ErrInvalidStatus    = errors.New("invalid status")
    ErrEmptyFunctionCall = errors.New("empty function call")
    ErrMissingAttestations = errors.New("missing attestations")
)

// Validation methods
func (o *ObjectState) Validate() error {
    if len(o.Code) > MaxCodeSize {
        return ErrCodeTooLarge
    }
    if len(o.Storage) > MaxStorageSize {
        return ErrStorageTooLarge
    }
    if o.RegionID == "" {
        return ErrInvalidRegionID
    }
    if o.LastUpdated.IsZero() {
        return fmt.Errorf("last updated time cannot be zero")
    }
    if !isValidStatus(o.Status) {
        return ErrInvalidStatus
    }
    return nil
}

func (e *Event) Validate() error {
    if e.FunctionCall == "" {
        return ErrEmptyFunctionCall
    }
    if len(e.Parameters) > MaxStorageSize {
        return ErrStorageTooLarge
    }
    if len(e.Attestations) != 2 {
        return ErrMissingAttestations
    }
    
    // Validate attestations
    for _, att := range e.Attestations {
        if err := att.Validate(); err != nil {
            return fmt.Errorf("invalid attestation: %w", err)
        }
    }
    
    // Verify attestation timestamps match
    if e.Attestations[0].GetTimeUTC() != e.Attestations[1].GetTimeUTC() {
        return errors.New("attestation timestamps do not match")
    }
    
    return nil
}

// Helper functions
func CreateEventID(objectID string, attestation TEEAttestation) string {
    return fmt.Sprintf("%s:%s", objectID, attestation.GetTimeUTC())
}

func isValidStatus(status string) bool {
    switch status {
    case StatusActive, StatusPending, StatusComplete, StatusError:
        return true
    default:
        return false
    }
}

// Clone methods to prevent unintended mutations
func (o *ObjectState) Clone() *ObjectState {
    clone := &ObjectState{
        RegionID:    o.RegionID,
        LastUpdated: o.LastUpdated,
        Status:      o.Status,
    }
    
    if o.Code != nil {
        clone.Code = make([]byte, len(o.Code))
        copy(clone.Code, o.Code)
    }
    
    if o.Storage != nil {
        clone.Storage = make([]byte, len(o.Storage))
        copy(clone.Storage, o.Storage)
    }
    
    if o.Events != nil {
        clone.Events = make([]string, len(o.Events))
        copy(clone.Events, o.Events)
    }
    
    return clone
}

func (e *Event) Clone() *Event {
    clone := &Event{
        FunctionCall: e.FunctionCall,
        Timestamp:    e.Timestamp,
        Status:       e.Status,
    }
    
    if e.Parameters != nil {
        clone.Parameters = make([]byte, len(e.Parameters))
        copy(clone.Parameters, e.Parameters)
    }
    
    // Clone attestations
    clone.Attestations = [2]TEEAttestation{
        e.Attestations[0],
        e.Attestations[1],
    }
    
    return clone
}

// Status update helpers
func (o *ObjectState) UpdateStatus(newStatus string) error {
    if !isValidStatus(newStatus) {
        return ErrInvalidStatus
    }
    o.Status = newStatus
    o.LastUpdated = time.Now().UTC()
    return nil
}

func (e *Event) UpdateStatus(newStatus string) error {
    if !isValidStatus(newStatus) {
        return ErrInvalidStatus
    }
    e.Status = newStatus
    return nil
}

// Event helper methods
func (e *Event) GetPrimaryAttestation() *TEEAttestation {
    return &e.Attestations[0]
}

func (e *Event) GetSecondaryAttestation() *TEEAttestation {
    return &e.Attestations[1]
}

func (e *Event) VerifyAttestations() error {
    if !e.Attestations[0].Equal(&e.Attestations[1]) {
        return errors.New("attestation mismatch")
    }
    return nil
}