// tee/types.go
package tee

import (
    pb "github.com/rhombus-tech/vm/tee"
    "github.com/rhombus-tech/vm/actions"
)

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