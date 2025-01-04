// tee/types.go
package tee

import (
    "time"

    pb "github.com/rhombus-tech/vm/tee"
    "github.com/rhombus-tech/vm/actions"
)


// ShuttleEvent represents an event with proper time handling
type ShuttleEvent struct {
    ID           string
    FunctionCall string
    Parameters   []byte
    Timestamp    time.Time
    Attestations [2]TEEAttestation
    RegionID     string
}

// Convert protocol buffer event to internal format
func convertEvent(event *pb.Event) (*ShuttleEvent, error) {
    // Parse timestamp string to time.Time
    parsedTime, err := time.Parse(time.RFC3339, event.Timestamp)
    if err != nil {
        return nil, fmt.Errorf("failed to parse timestamp: %w", err)
    }

    attestations := convertProtoAttestations(event.Attestations)

    return &ShuttleEvent{
        ID:           event.Id,
        FunctionCall: event.FunctionCall,
        Parameters:   event.Parameters,
        Timestamp:    parsedTime.UTC(),
        Attestations: attestations,
        RegionID:     event.RegionId,
    }, nil
}

// Convert between proto and VM types
func toProtoAttestation(att actions.TEEAttestation) *pb.TEEAttestation {
    return &pb.TEEAttestation{
        EnclaveId:   att.EnclaveID,
        Measurement: att.Measurement,
        Timestamp:   att.Timestamp,
        Data:        att.Data,
        Signature:   att.Signature,
        RegionProof: att.RegionProof,
    }
}

func fromProtoAttestation(att *pb.TEEAttestation) actions.TEEAttestation {
    return actions.TEEAttestation{
        EnclaveID:   att.EnclaveId,
        Measurement: att.Measurement,
        Timestamp:   att.Timestamp,
        Data:        att.Data,
        Signature:   att.Signature,
        RegionProof: att.RegionProof,
    }
}