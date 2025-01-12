// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package vm

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	"github.com/ava-labs/avalanchego/version"
	"github.com/ava-labs/hypersdk/auth"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/codec"
	sdkvm "github.com/ava-labs/hypersdk/vm"

	"github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/verifier"
)

var (
    ActionParser *codec.TypeParser[chain.Action]
    AuthParser   *codec.TypeParser[chain.Auth]
    OutputParser *codec.TypeParser[codec.Typed]
)

type validatorManager struct {
    validators  map[string]*verifier.StateVerifier
    validatorMu sync.RWMutex
}

// Setup types
func init() {
    ActionParser = codec.NewTypeParser[chain.Action]()
    AuthParser = codec.NewTypeParser[chain.Auth]()
    OutputParser = codec.NewTypeParser[codec.Typed]()

    errs := &wrappers.Errs{}
    errs.Add(
        // When registering new actions, ALWAYS make sure to append at the end.
        ActionParser.Register(&actions.CreateObjectAction{}, nil),
        ActionParser.Register(&actions.SendEventAction{}, nil),
        ActionParser.Register(&actions.SetInputObjectAction{}, nil),
        
        // When registering new auth, ALWAYS make sure to append at the end.
        AuthParser.Register(&auth.ED25519{}, auth.UnmarshalED25519),
        AuthParser.Register(&auth.SECP256R1{}, auth.UnmarshalSECP256R1),
        AuthParser.Register(&auth.BLS{}, auth.UnmarshalBLS),

        OutputParser.Register(&actions.CreateObjectResult{}, nil),
        OutputParser.Register(&actions.SendEventResult{}, nil),
        OutputParser.Register(&actions.SetInputObjectResult{}, nil),

        ActionParser.Register(&actions.UpdateValidEnclavesAction{}, nil),
        OutputParser.Register(&actions.UpdateValidEnclavesResult{}, nil),
    )
    if errs.Errored() {
        panic(errs.Err)
    }
}

// Add this method to the existing ShuttleVM
func (vm *ShuttleVM) initializeValidators() {
    vm.validatorMgr = &validatorManager{
        validators: make(map[string]*verifier.StateVerifier),
    }

    // Create separate validator for each region
    for _, region := range vm.config.Regions {
        validator := verifier.New(vm.stateManager)
        vm.validatorMgr.validators[region.ID] = validator
    }
}

// Add regional validation to existing ValidateTransaction
func (vm *ShuttleVM) validateRegionalActions(ctx context.Context, tx *chain.Transaction) error {
    // Group actions by region
    regionActions := make(map[string][]chain.Action)
    
    for _, action := range tx.Actions {
        if regionalAction, ok := action.(interface{ GetRegionID() string }); ok {
            regionID := regionalAction.GetRegionID()
            regionActions[regionID] = append(regionActions[regionID], action)
        }
    }

    // Validate actions for each region in parallel
    var wg sync.WaitGroup
    errChan := make(chan error, len(regionActions))

    for regionID, actions := range regionActions {
        wg.Add(1)
        go func(rid string, acts []chain.Action) {
            defer wg.Done()
            
            vm.validatorMgr.validatorMu.RLock()
            validator := vm.validatorMgr.validators[rid]
            vm.validatorMgr.validatorMu.RUnlock()

            if validator == nil {
                errChan <- fmt.Errorf("no validator for region %s", rid)
                return
            }

            for _, act := range acts {
                if err := validator.VerifyStateTransition(ctx, act); err != nil {
                    errChan <- fmt.Errorf("validation failed for region %s: %w", rid, err)
                    return
                }
            }
        }(regionID, actions)
    }

    // Wait for all validations to complete
    wg.Wait()
    close(errChan)

    // Check for any errors
    for err := range errChan {
        if err != nil {
            return err
        }
    }

    return nil
}

func With(name string, defaultValue interface{}) sdkvm.Option {
    return sdkvm.NewOption(
        name,
        defaultValue,
        func(v *sdkvm.VM, cfg interface{}) error {
            fmt.Printf("Received config for [%s]: %v\n", name, cfg)
            return nil
        },
    )
}


type StateSyncableVM interface {
    StateSync() error
}

var _ StateSyncableVM = &ShuttleVM{}

// Add the single required method
func (vm *ShuttleVM) StateSync() error {
    return nil
}

// You can remove these since they're not part of the interface:
// - StateSyncEnabled
// - GetStateSummary
// - ParseStateSummary
// - StateSummary struct and its methods

// Keep all these other methods as they're likely part of other interfaces:
func (vm *ShuttleVM) Connected(ctx context.Context, nodeID ids.NodeID, version *version.Application) error {
    return nil
}

func (vm *ShuttleVM) Disconnected(ctx context.Context, nodeID ids.NodeID) error {
    return nil
}

func (vm *ShuttleVM) AppRequest(ctx context.Context, nodeID ids.NodeID, requestID uint32, deadline time.Time, request []byte) error {
    return nil
}

func (vm *ShuttleVM) AppResponse(ctx context.Context, nodeID ids.NodeID, requestID uint32, response []byte) error {
    return nil
}

func (vm *ShuttleVM) AppRequestFailed(ctx context.Context, nodeID ids.NodeID, requestID uint32, err *common.AppError) error {
    return nil
}

func (vm *ShuttleVM) AppGossip(ctx context.Context, nodeID ids.NodeID, msg []byte) error {
    return nil
}

func (vm *ShuttleVM) Version(context.Context) (string, error) {
    return "0.0.1", nil
}

func (vm *ShuttleVM) CreateHandlers(context.Context) (map[string]http.Handler, error) {
    return map[string]http.Handler{}, nil
}

func (vm *ShuttleVM) CreateStaticHandlers(context.Context) (map[string]http.Handler, error) {
    return map[string]http.Handler{}, nil
}

// Block represents a basic block without snowman consensus 
type Block struct {
    id        ids.ID
    timestamp time.Time
}

func (vm *ShuttleVM) BuildBlock(ctx context.Context) (snowman.Block, error) {
    return nil, fmt.Errorf("not implemented: BuildBlock")
}

func (vm *ShuttleVM) GetBlockIDAtHeight(ctx context.Context, height uint64) (ids.ID, error) {
    select {
    case <-ctx.Done():
        return ids.Empty, ctx.Err()
    default:
    }

    heightKey := []byte(fmt.Sprintf("height-%d", height))
    blockIDBytes, err := vm.db.Get(heightKey)
    if err == database.ErrNotFound {
        return ids.Empty, database.ErrNotFound
    }
    if err != nil {
        return ids.Empty, err
    }

    return ids.ToID(blockIDBytes)
}

func (vm *ShuttleVM) SetState(context.Context, snow.State) error {
    return nil
}

func (vm *ShuttleVM) LastAccepted(context.Context) (ids.ID, error) {
    return ids.Empty, nil
}

func (vm *ShuttleVM) GetBlock(context.Context, ids.ID) (snowman.Block, error) {
    return nil, fmt.Errorf("not implemented: GetBlock")
}

func (vm *ShuttleVM) ParseBlock(context.Context, []byte) (snowman.Block, error) {
    return nil, fmt.Errorf("not implemented: ParseBlock") 
}

func (vm *ShuttleVM) SetPreference(context.Context, ids.ID) error {
    return nil
}

func (vm *ShuttleVM) HealthCheck(context.Context) (interface{}, error) {
    // Basic health response
    health := map[string]interface{}{
        "healthy": true,
    }
    return health, nil
}