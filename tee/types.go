// tee/types.go
package tee

import (
    "fmt"
    "time"

    "github.com/rhombus-tech/vm/tee/proto"
    "github.com/rhombus-tech/vm/core"
)

const (
    TEETypeSGX = "SGX"
    TEETypeSEV = "SEV"
)

// TEEPairInfo represents metadata about a TEE pair
type TEEPairInfo struct {
    ID          string `json:"id"`
    SGXEndpoint string `json:"sgx_endpoint"`
    SEVEndpoint string `json:"sev_endpoint"`
    Status      string `json:"status"`
    Attestations [2]core.TEEAttestation `json:"attestations,omitempty"`
}

// TEEPairConfig defines configuration for a TEE pair
type TEEPairConfig struct {
    ID          string        `json:"id"`
    SGXEndpoint string        `json:"sgx_endpoint"`
    SEVEndpoint string        `json:"sev_endpoint"`
    Thresholds  Thresholds    `json:"thresholds"`
}

// Thresholds defines operational thresholds for a TEE pair
type Thresholds struct {
    MinSuccessRate float64       `json:"min_success_rate"`
    MaxErrorRate   float64       `json:"max_error_rate"`
    MaxLatency     time.Duration `json:"max_latency"`
    MaxLoadFactor  float64       `json:"max_load_factor"`
}

// TEEPairMetrics holds runtime metrics for a TEE pair
type TEEPairMetrics struct {
    PairID          string        `json:"pair_id"`
    SGXEndpoint     string        `json:"sgx_endpoint"`
    SEVEndpoint     string        `json:"sev_endpoint"`
    LastHealthCheck time.Time     `json:"last_health_check"`
    LastHealthy     time.Time     `json:"last_healthy"`
    SuccessRate     float64       `json:"success_rate"`
    LoadFactor      float64       `json:"load_factor"`
    ExecutionTime   time.Duration `json:"execution_time"`
    ConsecutiveErrors uint64      `json:"consecutive_errors"`
}


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
func convertEvent(event *proto.Event) (*ShuttleEvent, error) {
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
func toProtoAttestation(att core.TEEAttestation) *proto.TEEAttestation {
    return &proto.TEEAttestation{
        EnclaveId:   att.EnclaveID,
        Measurement: att.Measurement,
        Timestamp:   att.Timestamp.Format(time.RFC3339),
        Data:        att.Data,
        Signature:   att.Signature,
        RegionProof: att.RegionProof,
    }
}

func fromProtoAttestation(att *proto.TEEAttestation) (core.TEEAttestation, error) {
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
func convertProtoAttestations(protoAtts []*proto.TEEAttestation) ([2]core.TEEAttestation, error) {
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
func (e *ShuttleEvent) ToProto() *proto.Event {
    protoAtts := make([]*proto.TEEAttestation, 2)
    for i, att := range e.Attestations {
        protoAtts[i] = toProtoAttestation(att)
    }

    return &proto.Event{
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

// Convert from proto TEEAttestation to core TEEAttestation
func protoToCoreAttestation(proto *proto.TEEAttestation) (core.TEEAttestation, error) {
    timestamp, err := time.Parse(time.RFC3339, proto.Timestamp)
    if err != nil {
        return core.TEEAttestation{}, err
    }
    
    return core.TEEAttestation{
        EnclaveID:   proto.EnclaveId,
        Measurement: proto.Measurement,
        Timestamp:   timestamp,
        Data:        proto.Data,
        Signature:   proto.Signature,
        RegionProof: proto.RegionProof,
    }, nil
}

// Convert slice of proto attestations to fixed-size array of core attestations
func protoToCoreAttestations(protos []*proto.TEEAttestation) ([2]core.TEEAttestation, error) {
    if len(protos) != 2 {
        return [2]core.TEEAttestation{}, fmt.Errorf("expected 2 attestations, got %d", len(protos))
    }
    
    var result [2]core.TEEAttestation
    for i, proto := range protos {
        converted, err := protoToCoreAttestation(proto)
        if err != nil {
            return [2]core.TEEAttestation{}, err
        }
        result[i] = converted
    }
    
    return result, nil
}