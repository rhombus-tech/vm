// server.go
package vm

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ava-labs/hypersdk/api"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/genesis"

	"github.com/rhombus-tech/vm/consts"
	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/regions"
)

// JSONRPCEndpoint is the path for JSON RPC requests (e.g. /morpheusapi).
const JSONRPCEndpoint = "/morpheusapi"

// jsonRPCServerFactory implements the HyperSDK API factory pattern.
type jsonRPCServerFactory struct{}

var _ api.HandlerFactory[chain.VM] = (*jsonRPCServerFactory)(nil)

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

type JSONRPCServer struct {
    vm chain.VM
}

// Request/Response Types
type GenesisReply struct {
    Genesis *genesis.DefaultGenesis `json:"genesis,omitempty"`
}

type BalanceArgs struct {
    Address codec.Address `json:"address"`
}

type BalanceReply struct {
    Amount uint64 `json:"amount"`
}

type ExecuteArgs struct {
    RegionID     string `json:"region_id"`  
    IDTo         string `json:"id_to"`
    FunctionCall string `json:"function_call"`
    Parameters   []byte `json:"parameters"`
}

type ExecuteResult struct {
    Success    bool   `json:"success"`
    StateHash  []byte `json:"state_hash"`
    Output     []byte `json:"output"`
    Timestamp  string `json:"timestamp"`
}

type GetRegionHealthRequest struct {
    RegionID string `json:"region_id"`
}

type GetRegionHealthResponse struct {
    Health *regions.HealthStatus `json:"health"`
}

type GetRegionMetricsRequest struct {
    RegionID string `json:"region_id"`
    PairID   string `json:"pair_id"`
}

type GetRegionMetricsResponse struct {
    Metrics *regions.TEEPairMetrics `json:"metrics"`
}

// Genesis is an example JSON-RPC method that tries to retrieve a DefaultGenesis.
func (j *JSONRPCServer) Genesis(_ *http.Request, _ *struct{}, reply *GenesisReply) error {
    vmWithGenesis, ok := j.vm.(interface {
        MyCustomGenesis() (*genesis.DefaultGenesis, error)
    })
    if !ok {
        return nil
    }

    defGenesis, err := vmWithGenesis.MyCustomGenesis()
    if err != nil {
        return err
    }
    reply.Genesis = defGenesis
    return nil
}

// Balance is a JSON-RPC method for retrieving a user's balance.
func (j *JSONRPCServer) Balance(req *http.Request, args *BalanceArgs, reply *BalanceReply) error {
    vmWithBalance, ok := j.vm.(interface {
        ReadBalance(context.Context, []byte) (uint64, error)
    })
    if !ok {
        return errors.New("balance reading not supported")
    }

    amount, err := vmWithBalance.ReadBalance(req.Context(), args.Address[:])
    if err != nil {
        return err
    }
    reply.Amount = amount
    return nil
}

// Execute handles execution requests
func (j *JSONRPCServer) Execute(req *http.Request, args *ExecuteArgs, reply *ExecuteResult) error {
    if args.RegionID == "" {
        return errors.New("region ID required")
    }

    vmWithRegions, ok := j.vm.(interface {
        ExecuteInRegion(context.Context, string, string, string, []byte) ([]byte, []byte, string, error)
    })
    if !ok {
        return errors.New("regional execution not supported")
    }

    stateHash, output, timestamp, err := vmWithRegions.ExecuteInRegion(
        req.Context(),
        args.RegionID,
        args.IDTo,
        args.FunctionCall,
        args.Parameters,
    )
    if err != nil {
        return err
    }

    reply.Success = true
    reply.StateHash = stateHash
    reply.Output = output
    reply.Timestamp = timestamp

    return nil
}

// GetObject retrieves object state
func (j *JSONRPCServer) GetObject(r *http.Request, args *struct {
    ObjectID string `json:"object_id"`
    RegionID string `json:"region_id"`
}, reply *struct {
    Object *core.ObjectState `json:"object"`
}) error {
    vmWithObjects, ok := j.vm.(interface {
        GetObject(context.Context, string, string) (*core.ObjectState, error)
    })
    if !ok {
        return fmt.Errorf("VM does not implement GetObject")
    }

    obj, err := vmWithObjects.GetObject(r.Context(), args.ObjectID, args.RegionID)
    if err != nil {
        return err
    }

    reply.Object = obj
    return nil
}

// GetValidEnclave handles enclave.get RPC requests
func (j *JSONRPCServer) GetValidEnclave(r *http.Request, args *struct {
    EnclaveID string `json:"enclave_id"`
    RegionID  string `json:"region_id"`
}, reply *struct {
    EnclaveInfo *core.EnclaveInfo `json:"enclave_info"`
}) error {
    vmWithEnclave, ok := j.vm.(interface {
        GetValidEnclave(context.Context, string, string) (*core.EnclaveInfo, error)
    })
    if !ok {
        return fmt.Errorf("VM does not implement GetValidEnclave")
    }

    info, err := vmWithEnclave.GetValidEnclave(r.Context(), args.EnclaveID, args.RegionID)
    if err != nil {
        return err
    }

    reply.EnclaveInfo = info
    return nil
}

// GetRegionHealth retrieves health status for a region
func (j *JSONRPCServer) GetRegionHealth(r *http.Request, args *GetRegionHealthRequest, reply *GetRegionHealthResponse) error {
    vmWithRegions, ok := j.vm.(interface {
        GetRegionHealth(context.Context, string) (*regions.HealthStatus, error)
    })
    if !ok {
        return fmt.Errorf("VM does not implement GetRegionHealth")
    }

    health, err := vmWithRegions.GetRegionHealth(r.Context(), args.RegionID)
    if err != nil {
        return err
    }
    reply.Health = health
    return nil
}

// GetRegionMetrics retrieves metrics for a region/pair
func (j *JSONRPCServer) GetRegionMetrics(r *http.Request, args *GetRegionMetricsRequest, reply *GetRegionMetricsResponse) error {
    vmWithMetrics, ok := j.vm.(interface {
        GetRegionMetrics(context.Context, string, string) (*regions.TEEPairMetrics, error)
    })
    if !ok {
        return fmt.Errorf("VM does not implement GetRegionMetrics")
    }

    metrics, err := vmWithMetrics.GetRegionMetrics(r.Context(), args.RegionID, args.PairID)
    if err != nil {
        return err
    }
    reply.Metrics = metrics
    return nil
}