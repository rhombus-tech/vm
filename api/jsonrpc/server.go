// api/jsonrpc/server.go

package jsonrpc

import (
	"context"
	"fmt"
	"net/http"

	"github.com/rhombus-tech/vm/core"
)

// Querier interfaces for different functionalities
type ObjectQuerier interface {
    GetObject(ctx context.Context, objectID string, regionID string) (*core.ObjectState, error)
    GetValidEnclave(ctx context.Context, enclaveID string, regionID string) (*core.EnclaveInfo, error)
}

type CoordinationQuerier interface {
    GetRegionWorkers(ctx context.Context, regionID string) ([]*Worker, error)
    GetRegionTasks(ctx context.Context, regionID string) ([]*Task, error)
}

// Backend interface combines all functionality
type Backend interface {
    ObjectQuerier
    CoordinationQuerier
}

// JSONRPCServer handles all RPC requests
type JSONRPCServer struct {
    backend Backend
}

// Create new server instance
func NewJSONRPCServer(backend Backend) *JSONRPCServer {
    return &JSONRPCServer{
        backend: backend,
    }
}

// Object-related handlers
func (s *JSONRPCServer) GetObject(r *http.Request, args *GetObjectRequest, reply *GetObjectResponse) error {
    obj, err := s.backend.GetObject(r.Context(), args.ObjectID, args.RegionID)
    if err != nil {
        return fmt.Errorf("failed to get object: %w", err)
    }
    reply.Object = obj
    return nil
}

func (s *JSONRPCServer) GetValidEnclave(r *http.Request, args *GetEnclaveRequest, reply *GetEnclaveResponse) error {
    info, err := s.backend.GetValidEnclave(r.Context(), args.EnclaveID, args.RegionID)
    if err != nil {
        return fmt.Errorf("failed to get enclave info: %w", err)
    }
    reply.EnclaveInfo = info
    return nil
}

// Coordination handlers
func (s *JSONRPCServer) GetRegionWorkers(r *http.Request, args *GetRegionWorkersRequest, reply *GetRegionWorkersResponse) error {
    workers, err := s.backend.GetRegionWorkers(r.Context(), args.RegionID)
    if err != nil {
        return fmt.Errorf("failed to get workers: %w", err)
    }
    reply.Workers = workers
    return nil
}

func (s *JSONRPCServer) GetRegionTasks(r *http.Request, args *GetRegionTasksRequest, reply *GetRegionTasksResponse) error {
    tasks, err := s.backend.GetRegionTasks(r.Context(), args.RegionID)
    if err != nil {
        return fmt.Errorf("failed to get tasks: %w", err)
    }
    reply.Tasks = tasks
    return nil
}