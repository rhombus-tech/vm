// server.go
package vm

import (
	"context"
	"errors"
	"net/http"

	// Provided by HyperSDK
	"github.com/ava-labs/hypersdk/api"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/genesis"

	// Local packages
	"github.com/rhombus-tech/vm/consts"
)

// JSONRPCEndpoint is the path for JSON RPC requests (e.g. /morpheusapi).
const JSONRPCEndpoint = "/morpheusapi"

// jsonRPCServerFactory implements the HyperSDK API factory pattern.
//
// The generic type here is [chain.VM], so AddAPIHandler(...) can accept it.
type jsonRPCServerFactory struct{}

var _ api.HandlerFactory[chain.VM] = (*jsonRPCServerFactory)(nil)

// New is required by api.HandlerFactory. It should return an api.Handler
// with a path and the underlying http.Handler.
func (jsonRPCServerFactory) New(vm chain.VM) (api.Handler, error) {
    handler, err := api.NewJSONRPCHandler(consts.Name, &JSONRPCServer{vm: vm})
    if err != nil {
        return api.Handler{}, err
    }
    return api.Handler{
        Path:    JSONRPCEndpoint,
        Handler: handler,
    }, nil
}

// JSONRPCServer implements your JSON-RPC methods.
type JSONRPCServer struct {
    vm chain.VM
}

// GenesisReply is returned from the Genesis method.
type GenesisReply struct {
    Genesis *genesis.DefaultGenesis `json:"genesis,omitempty"`
}

// Genesis is an example JSON-RPC method that tries to retrieve a DefaultGenesis.
func (j *JSONRPCServer) Genesis(_ *http.Request, _ *struct{}, reply *GenesisReply) error {
    // Cast chain.VM to something that can provide Genesis (e.g. MyVM).
    vmWithGenesis, ok := j.vm.(interface {
        MyCustomGenesis() (*genesis.DefaultGenesis, error)
    })
    if !ok {
        // Not implemented; just return nil or an error
        return nil
    }

    defGenesis, err := vmWithGenesis.MyCustomGenesis()
    if err != nil {
        return err
    }
    reply.Genesis = defGenesis
    return nil
}

// BalanceArgs for the Balance method.
type BalanceArgs struct {
    Address codec.Address `json:"address"`
}

// BalanceReply for the Balance method.
type BalanceReply struct {
    Amount uint64 `json:"amount"`
}

// Balance is an example JSON-RPC method for retrieving a user’s balance.
func (j *JSONRPCServer) Balance(req *http.Request, args *BalanceArgs, reply *BalanceReply) error {
    ctx := req.Context()

    // If your chain.VM implements a method to read balances, cast to it.
    vmWithBalance, ok := j.vm.(interface {
        ReadBalance(context.Context, []byte) (uint64, error)
    })
    if !ok {
        // If no method is available, you can call your storage package directly,
        // but you must pass the correct interface or DB reference.
        // Example, if you want to do storage.GetBalance(ctx, ???, args.Address)
        // For now, we'll just short-circuit
        return nil
    }

    amount, err := vmWithBalance.ReadBalance(ctx, args.Address[:])
    if err != nil {
        return err
    }
    reply.Amount = amount
    return nil
}

// ExecuteArgs for the Execute method
type ExecuteArgs struct {
    RegionID     string        `json:"region_id"`  
    IDTo         string        `json:"id_to"`
    FunctionCall string        `json:"function_call"`
    Parameters   []byte        `json:"parameters"`
}

// ExecuteResult for the Execute method
type ExecuteResult struct {
    Success    bool   `json:"success"`
    StateHash  []byte `json:"state_hash"`
    Output     []byte `json:"output"`
    Timestamp  string `json:"timestamp"`
}

// Add this new method after the existing Balance method
func (j *JSONRPCServer) Execute(req *http.Request, args *ExecuteArgs, reply *ExecuteResult) error {
    ctx := req.Context()

    // Validate region exists and operation is authorized
    if args.RegionID == "" {
        return errors.New("region ID required")
    }

    // Cast chain.VM to something that can execute in regions
    vmWithRegions, ok := j.vm.(interface {
        ExecuteInRegion(context.Context, string, string, string, []byte) ([]byte, []byte, string, error)
    })
    if !ok {
        return errors.New("regional execution not supported")
    }

    // Execute the operation
    stateHash, output, timestamp, err := vmWithRegions.ExecuteInRegion(
        ctx,
        args.RegionID,
        args.IDTo,
        args.FunctionCall,
        args.Parameters,
    )
    if err != nil {
        return err
    }

    // Fill the reply
    reply.Success = true
    reply.StateHash = stateHash
    reply.Output = output
    reply.Timestamp = timestamp

    return nil
}