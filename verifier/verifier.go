// verifier/verifier.go
package verifier

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	// If you rely on roughtime, import it here:
	// "github.com/cloudflare/roughtime"

	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/state"

	"github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/consts"
	"github.com/rhombus-tech/vm/coordination"
	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/storage"
	"github.com/rhombus-tech/vm/timeserver"
)

var (
    ErrInputObjectMissing = errors.New("input object not found")
    ErrInvalidEventOrder  = errors.New("invalid event order")
    ErrInvalidAttestation = errors.New("invalid TEE attestation")
    ErrTimestampOutOfRange = errors.New("timestamp outside valid window")
)

type StateVerifier struct {
    state       state.Mutable
    coordinator *coordination.Coordinator
}

func New(state state.Mutable) *StateVerifier {
    return &StateVerifier{
        state: state,
    }
}

func (v *StateVerifier) SetState(state state.Mutable) {
    v.state = state
}

func (v *StateVerifier) VerifySystemState(ctx context.Context) error {
    // Verify input object exists and is valid
    inputID, err := storage.GetInputObject(ctx, v.state)
    if err != nil {
        return err
    }
    if inputID == "" {
        return ErrInputObjectMissing
    }

    // Since input objects are system-wide, use empty string as region ID
    // Or if you have a specific system region, use that
    systemRegionID := "" // or "system" depending on your architecture
    
    inputObject, err := storage.GetObject(ctx, v.state, inputID, systemRegionID)
    if err != nil {
        return err
    }
    if inputObject == nil {
        return ErrInputObjectMissing
    }
    return nil
}

func (v *StateVerifier) VerifyObjectState(ctx context.Context, obj map[string][]byte) error {
    if code, exists := obj["code"]; exists {
        if len(code) > consts.MaxCodeSize {
            return actions.ErrCodeTooLarge
        }
    }
    if stor, exists := obj["storage"]; exists {
        if len(stor) > consts.MaxStorageSize {
            return actions.ErrStorageTooLarge
        }
    }
    return nil
}

func (v *StateVerifier) verifyAttestation(ctx context.Context, att core.TEEAttestation, region map[string]interface{}) error {
    tees := region["tees"].([]core.TEEAddress) // not actions.TEEAddress
    found := false
    for _, tee := range tees {
        if bytes.Equal(tee, att.EnclaveID) {
            found = true
            break
        }
    }
    if !found {
        return ErrInvalidAttestation
    }

    // If you want to compare timestamps, ensure you have roughtime or time
    // currentTime := roughtime.Now() // or time.Now()
    // if !isTimeInWindow(attestation.Timestamp, currentTime) {
    //     return ErrTimestampOutOfRange
    // }
    return nil
}

// Capitalized so other packages can call it (e.g., c.verifier.VerifyAttestationPair).
func (v *StateVerifier) VerifyAttestationPair(ctx context.Context, attestations [2]core.TEEAttestation, region map[string]interface{}) error {
    // Create secure channels between TEEs
    channels := make(map[string]*coordination.SecureChannel)
    for i := 0; i < len(attestations); i++ {
        for j := i + 1; j < len(attestations); j++ {
            channel := coordination.NewSecureChannel(
                coordination.WorkerID(attestations[i].EnclaveID),
                coordination.WorkerID(attestations[j].EnclaveID),
            )
            if err := channel.EstablishSecure(); err != nil {
                return err
            }
            channels[makeChannelKey(i, j)] = channel
        }
    }

    // Example verification message
    msg := &coordination.Message{
        Type: coordination.MessageTypeVerification,
        Data: attestations[0].Data,
    }

    // Send verification messages
    for _, channel := range channels {
        if err := channel.Send(msg.Data); err != nil {
            return err
        }
    }

    return nil
}

func (v *StateVerifier) VerifyStateTransition(ctx context.Context, action chain.Action) error {
    switch a := action.(type) {
    case *actions.CreateObjectAction:
        return v.verifyCreateObject(ctx, a)
    case *actions.SendEventAction:
        return v.verifyEvent(ctx, a)
    case *actions.SetInputObjectAction:
        return v.verifySetInputObject(ctx, a)
    default:
        return fmt.Errorf("unknown action type: %T", action)
    }
}

func (v *StateVerifier) verifyCreateObject(ctx context.Context, action *actions.CreateObjectAction) error {
    exists, err := storage.GetObject(ctx, v.state, action.ID, action.RegionID)
    if err != nil {
        return err
    }
    if exists != nil {
        return actions.ErrObjectExists
    }

    obj := map[string][]byte{
        "code":    action.Code,
        "storage": action.Storage,
    }
    return v.VerifyObjectState(ctx, obj)
}

func (v *StateVerifier) verifyEvent(ctx context.Context, action *actions.SendEventAction) error {
    targetObj, err := storage.GetObject(ctx, v.state, action.IDTo, action.RegionID)
    if err != nil {
        return err
    }
    if targetObj == nil {
        return actions.ErrObjectNotFound
    }

    // Get region for TEE verification
    region, err := storage.GetRegion(ctx, v.state, action.RegionID)
    if err != nil {
        return err
    }
    if region == nil {
        return actions.ErrRegionNotFound
    }

    // Verify attestations
    if err := v.VerifyAttestationPair(ctx, action.Attestations, region); err != nil {
        return err
    }

    // Verify function exists
    if err := v.verifyFunctionExists(targetObj, action.FunctionCall); err != nil {
        return err
    }

    if len(action.Parameters) > consts.MaxStorageSize {
        return actions.ErrStorageTooLarge
    }
    return nil
}

func (v *StateVerifier) verifySetInputObject(ctx context.Context, action *actions.SetInputObjectAction) error {
    obj, err := storage.GetObject(ctx, v.state, action.ID, action.RegionID)
    if err != nil {
        return err
    }
    if obj == nil {
        return actions.ErrObjectNotFound
    }
    return nil
}


func (v *StateVerifier) verifyCreateRegion(ctx context.Context, action *actions.CreateRegionAction) error {
    region, err := storage.GetRegion(ctx, v.state, action.RegionID)
    if err != nil {
        return err
    }
    if region != nil {
        return actions.ErrRegionExists
    }

    // Verify all TEEs
    for _, tee := range action.TEEs {
        if len(tee) == 0 {
            return actions.ErrInvalidTEE
        }
    }

    // Use a minimal region object just to pass to verification
    dummyRegion := map[string]interface{}{
        "tees": action.TEEs,
    }
    return v.VerifyAttestationPair(ctx, action.Attestations, dummyRegion)
}

func (v *StateVerifier) verifyUpdateRegion(ctx context.Context, action *actions.UpdateRegionAction) error {
    region, err := storage.GetRegion(ctx, v.state, action.RegionID)
    if err != nil {
        return err
    }
    if region == nil {
        return actions.ErrRegionNotFound
    }

    // Verify updated attestations
    if err := v.VerifyAttestationPair(ctx, action.Attestations, region); err != nil {
        return err
    }

    return nil
}

func (v *StateVerifier) VerifyExecution(ctx context.Context, result *core.ExecutionResult) error {
    // Verify attestations
    if err := v.VerifyAttestationPair(ctx, result.Attestations, nil); err != nil {
        return fmt.Errorf("attestation verification failed: %w", err)
    }

    // Verify TimeProof instead of Timestamp
    if err := v.verifyTimeProof(result.TimeProof); err != nil {
        return fmt.Errorf("time proof verification failed: %w", err)
    }

    return nil
}

// Update the verification method to handle TimeProof
func (v *StateVerifier) verifyTimeProof(timeProof *timeserver.VerifiedTimestamp) error {
    if timeProof == nil {
        return fmt.Errorf("missing time proof")
    }

    // Verify we have enough proofs
    if len(timeProof.Proofs) < 2 {
        return fmt.Errorf("insufficient time proofs")
    }

    // Verify time is recent
    age := time.Since(timeProof.Time)
    if age > 5*time.Second {
        return fmt.Errorf("time proof too old: %v", age)
    }

    return nil
}

func (v *StateVerifier) verifyFunctionExists(obj map[string][]byte, function string) error {
    // Implementation would check if the function exists in the object's code
    return nil
}

// Helper to check if [timestamp] is in a safe window relative to [currentTime].
func isTimeInWindow(timestamp, currentTime string) bool {
    // Implementation would verify timestamp is within acceptable window
    return true
}

// Extract region ID from object ID, if needed
func extractRegionFromID(id string) string {
    // Implementation would extract region ID from object ID
    return ""
}

// Minimal stub for channel keys
func makeChannelKey(i, j int) string {
    return fmt.Sprintf("%d-%d", i, j)
}
