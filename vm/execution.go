// vm/execution.go
package vm

import (
    "context"
    "fmt"
    "time"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
    "github.com/rhombus-tech/vm/actions"
    "github.com/rhombus-tech/vm/core"
)

// RegionalExecutor handles region-specific execution
type RegionalExecutor struct {
    stateManager StateManager
}

func NewRegionalExecutor(sm StateManager) *RegionalExecutor {
    return &RegionalExecutor{
        stateManager: sm,
    }
}

// ExecuteAction handles execution with region awareness
func (re *RegionalExecutor) ExecuteAction(
    ctx context.Context,
    action chain.Action,
    mu state.Mutable,
    timestamp int64,
    actor codec.Address,
    txID ids.ID,
) (codec.Typed, error) {
    // Get region ID from action
    regionID := re.getActionRegion(action)
    if regionID == "" {
        return nil, fmt.Errorf("no region specified for action")
    }

    // Verify region exists
    region, err := re.stateManager.GetRegion(ctx, mu, regionID)
    if err != nil {
        return nil, err
    }
    if region == nil {
        return nil, fmt.Errorf("region %s not found", regionID)
    }

    // Execute based on action type
    switch a := action.(type) {
    case *actions.SendEventAction:
        return re.executeSendEvent(ctx, a, mu, timestamp)
    case *actions.CreateObjectAction:
        return re.executeCreateObject(ctx, a, mu, timestamp)
    default:
        return nil, fmt.Errorf("unsupported action type: %T", action)
    }
}

// Helper methods
func (re *RegionalExecutor) getActionRegion(action chain.Action) string {
    switch a := action.(type) {
    case *actions.SendEventAction:
        return a.RegionID
    case *actions.CreateObjectAction:
        return a.RegionID
    default:
        return ""
    }
}

func (re *RegionalExecutor) executeSendEvent(
    ctx context.Context,
    action *actions.SendEventAction,
    mu state.Mutable,
    timestamp int64,
) (codec.Typed, error) {
    // Check if target object exists
    targetObj, err := re.stateManager.GetObject(ctx, mu, action.IDTo, action.RegionID)
    if err != nil {
        return nil, err
    }
    if targetObj == nil {
        return nil, fmt.Errorf("target object not found in region")
    }

    // Create event
    event := &core.Event{
        FunctionCall: action.FunctionCall,
        Parameters:   action.Parameters,
        Attestations: action.Attestations,
        Timestamp:    action.Attestations[0].Timestamp.Format(time.RFC3339),
        Status:      "pending",
    }

    // Store event
    if err := re.stateManager.SetEvent(ctx, mu, action.IDTo, event, action.RegionID); err != nil {
        return nil, err
    }

    return &actions.SendEventResult{
        Success:   true,
        IDTo:      action.IDTo,
        StateHash: action.Attestations[0].Data,
        Timestamp: event.Timestamp,
        RegionID:  action.RegionID,
    }, nil
}

func (re *RegionalExecutor) executeCreateObject(
    ctx context.Context,
    action *actions.CreateObjectAction,
    mu state.Mutable,
    timestamp int64,
) (codec.Typed, error) {
    // Check if object already exists
    existingObj, err := re.stateManager.GetObject(ctx, mu, action.ID, action.RegionID)
    if err != nil {
        return nil, err
    }
    if existingObj != nil {
        return nil, fmt.Errorf("object already exists in region")
    }

    // Create object
    obj := &core.ObjectState{
        Code:     action.Code,
        Storage:  action.Storage,
        RegionID: action.RegionID,
        Status:   "active",
    }

    // Store object
    if err := re.stateManager.SetObject(ctx, mu, action.ID, obj); err != nil {
        return nil, err
    }

    return &actions.CreateObjectResult{
        ID:       action.ID,
        RegionID: action.RegionID,
    }, nil
}