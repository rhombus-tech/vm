// tee/types.go
package tee

import (
    "fmt"
    "time"

    pb "github.com/rhombus-tech/vm/tee/proto"
    "github.com/rhombus-tech/vm/core"
)

const (
    TEETypeSGX = "SGX"
    TEETypeSEV = "SEV"
)

// ShuttleEvent represents an event with proper time handling
type ShuttleEvent struct {
    ID           string
    FunctionCall string
    Parameters   []byte
    Timestamp    time.Time
    Attestations [2]core.TEEAttestation
    RegionID     string
}

// Convert protocol buffer event to internal format
func convertEvent(event *pb.Event) (*ShuttleEvent, error) {
    // Parse timestamp string to time.Time
    parsedTime, err := time.Parse(time.RFC3339, event.Timestamp)
    if err != nil {
        return nil, fmt.Errorf("failed to parse timestamp: %w", err)
    }

    // Convert the attestations
    attestations, err := convertProtoAttestations(event.Attestations)
    if err != nil {
        return nil, fmt.Errorf("failed to convert attestations: %w", err)
    }

    return &ShuttleEvent{
        ID:           event.Id,
        FunctionCall: event.FunctionCall,
        Parameters:   event.Parameters,
        Timestamp:    parsedTime.UTC(),
        Attestations: attestations,
        RegionID:     event.RegionId,
    }, nil
}

// Convert between proto and core types
func toProtoAttestation(att core.TEEAttestation) *pb.TEEAttestation {
    return &pb.TEEAttestation{
        EnclaveId:   att.EnclaveID,
        Measurement: att.Measurement,
        Timestamp:   att.Timestamp.Format(time.RFC3339),
        Data:        att.Data,
        Signature:   att.Signature,
        RegionProof: att.RegionProof,
    }
}

func fromProtoAttestation(att *pb.TEEAttestation) (core.TEEAttestation, error) {
    timestamp, err := time.Parse(time.RFC3339, att.Timestamp)
    if err != nil {
        return core.TEEAttestation{}, fmt.Errorf("invalid timestamp format: %w", err)
    }

    return core.TEEAttestation{
        EnclaveID:   att.EnclaveId,
        Measurement: att.Measurement,
        Timestamp:   timestamp,
        Data:        att.Data,
        Signature:   att.Signature,
        RegionProof: att.RegionProof,
    }, nil
}

// Helper function to convert slice of proto attestations
func convertProtoAttestations(protoAtts []*pb.TEEAttestation) ([2]core.TEEAttestation, error) {
    if len(protoAtts) != 2 {
        return [2]core.TEEAttestation{}, fmt.Errorf("expected 2 attestations, got %d", len(protoAtts))
    }

    var result [2]core.TEEAttestation
    for i, att := range protoAtts {
        converted, err := fromProtoAttestation(att)
        if err != nil {
            return [2]core.TEEAttestation{}, fmt.Errorf("failed to convert attestation %d: %w", i, err)
        }
        result[i] = converted
    }

    return result, nil
}

// Helper method to convert ShuttleEvent back to proto message
func (e *ShuttleEvent) ToProto() *pb.Event {
    protoAtts := make([]*pb.TEEAttestation, 2)
    for i, att := range e.Attestations {
        protoAtts[i] = toProtoAttestation(att)
    }

    return &pb.Event{
        Id:           e.ID,
        FunctionCall: e.FunctionCall,
        Parameters:   e.Parameters,
        Timestamp:    e.Timestamp.Format(time.RFC3339),
        Attestations: protoAtts,
        RegionId:     e.RegionID,
    }
}

// Validation helper
func (e *ShuttleEvent) Validate() error {
    if e.ID == "" {
        return fmt.Errorf("empty event ID")
    }
    if e.FunctionCall == "" {
        return fmt.Errorf("empty function call")
    }
    if e.RegionID == "" {
        return fmt.Errorf("empty region ID")
    }
    if e.Timestamp.IsZero() {
        return fmt.Errorf("invalid timestamp")
    }
    
    // Validate attestations
    for i, att := range e.Attestations {
        if err := att.Validate(); err != nil {
            return fmt.Errorf("invalid attestation %d: %w", i, err)
        }
    }

    return nil
}