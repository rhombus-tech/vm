// actions/enclave.go
package actions

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
    
    "github.com/rhombus-tech/vm"
    "github.com/rhombus-tech/vm/consts"
    "github.com/rhombus-tech/vm/core"
)

type EnclaveInfo struct {
    Measurement []byte    `serialize:"true" json:"measurement"`
    ValidFrom   time.Time `serialize:"true" json:"valid_from"`
    ValidUntil  time.Time `serialize:"true" json:"valid_until"`
    EnclaveType string    `serialize:"true" json:"enclave_type"`
    RegionID    string    `serialize:"true" json:"region_id"`
}

type UpdateValidEnclavesAction struct {
    EnclaveID  []byte      `serialize:"true" json:"enclave_id"`
    Info       EnclaveInfo `serialize:"true" json:"info"`
    RegionID   string      `serialize:"true" json:"region_id"`
}

type UpdateValidEnclavesResult struct {
    EnclaveID []byte `serialize:"true" json:"enclave_id"`
    RegionID  string `serialize:"true" json:"region_id"`
    Success   bool   `serialize:"true" json:"success"`
}

func (*UpdateValidEnclavesAction) GetTypeID() uint8 {
    return consts.UpdateValidEnclavesID
}

func (*UpdateValidEnclavesResult) GetTypeID() uint8 {
    return consts.UpdateValidEnclavesResultID
}

func (u *UpdateValidEnclavesAction) StateKeys(actor codec.Address) state.Keys {
    enclaveKey := fmt.Sprintf("enclave/%x", u.EnclaveID)
    return state.Keys{
        enclaveKey: state.Write,
        string([]byte(fmt.Sprintf("r/%s/info", u.RegionID))): state.Read,
    }
}

func (u *UpdateValidEnclavesAction) Execute(
    ctx context.Context,
    rules chain.Rules,
    mu state.Mutable,
    timestamp int64,
    actor codec.Address,
    txID ids.ID,
) (codec.Typed, error) {
    stateManager, ok := mu.(vm.StateManager)
    if !ok {
        return nil, fmt.Errorf("invalid state manager type")
    }

    // Verify region exists
    exists, err := stateManager.RegionExists(ctx, mu, u.RegionID)
    if err != nil {
        return nil, err
    }
    if !exists {
        return nil, ErrRegionNotFound
    }

    // Store enclave info as an object
    obj := &core.ObjectState{
        Storage:     marshalEnclaveInfo(&u.Info),
        RegionID:    u.RegionID,
        Status:      "active",
        LastUpdated: time.Unix(timestamp, 0).UTC(),
    }

    // Use enclave ID as object ID with special prefix
    enclaveObjID := fmt.Sprintf("enclave/%x", u.EnclaveID)
    if err := stateManager.SetObject(ctx, mu, enclaveObjID, obj); err != nil {
        return nil, fmt.Errorf("failed to store enclave info: %w", err)
    }

    return &UpdateValidEnclavesResult{
        EnclaveID: u.EnclaveID,
        RegionID:  u.RegionID,
        Success:   true,
    }, nil
}

// Helper function to marshal enclave info
func marshalEnclaveInfo(info *EnclaveInfo) []byte {
    data, err := json.Marshal(info)
    if err != nil {
        return nil
    }
    return data
}

// Helper function to unmarshal enclave info
func unmarshalEnclaveInfo(data []byte) (*EnclaveInfo, error) {
    var info EnclaveInfo
    if err := json.Unmarshal(data, &info); err != nil {
        return nil, err
    }
    return &info, nil
}
