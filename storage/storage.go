// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package storage

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/ava-labs/avalanchego/database"
	smath "github.com/ava-labs/avalanchego/utils/math"
	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/consts"
	"github.com/ava-labs/hypersdk/state"
	"github.com/rhombus-tech/vm/coordination"
)

type ReadState func(context.Context, [][]byte) ([][]byte, []error)

// State
// / (height) => store in root
//   -> [heightPrefix] => height
// 0x0/ (balance)
//   -> [owner] => balance
// 0x1/ (hypersdk-height)
// 0x2/ (hypersdk-timestamp)
// 0x3/ (hypersdk-fee)
// 0x4/ (object)
//   -> [id] => object
// 0x5/ (event)
//   -> [timestamp][id] => event
// 0x6/ (input)
//   -> input object id
// 0x7/ (region)
//   -> [id] => region data
// 0x8/ (coordination)
//   -> [id] => coordination state

const (
   // Active state
   balancePrefix   = 0x0
   heightPrefix    = 0x1
   timestampPrefix = 0x2
   feePrefix       = 0x3

   // ShuttleVM state
   objectPrefix    = 0x4
   eventPrefix     = 0x5
   inputPrefix     = 0x6
   regionPrefix    = 0x7
   coordPrefix     = 0x8
)

const BalanceChunks uint16 = 1

var (
   heightKey    = []byte{heightPrefix}
   timestampKey = []byte{timestampPrefix}
   feeKey      = []byte{feePrefix}

   ErrInvalidCoordination = errors.New("invalid coordination state")
)

// New coordination functions
type CoordinationState struct {
   Workers    []coordination.WorkerID `json:"workers"`
   Regions    []string               `json:"regions"`
   Tasks      map[string]TaskState   `json:"tasks"`
}

type TaskState struct {
   Status      string                `json:"status"`
   Workers     []coordination.WorkerID `json:"workers"`
   Attestations [][2][]byte          `json:"attestations"`
   Timestamp    string               `json:"timestamp"`
}

func CoordinationKey(id string) []byte {
   k := make([]byte, 1+len(id))
   k[0] = coordPrefix
   copy(k[1:], []byte(id))
   return k
}

func GetCoordinationState(
   ctx context.Context,
   im state.Immutable,
) (*CoordinationState, error) {
   k := []byte{coordPrefix}
   v, err := im.GetValue(ctx, k)
   if errors.Is(err, database.ErrNotFound) {
       return &CoordinationState{
           Tasks: make(map[string]TaskState),
       }, nil
   }
   if err != nil {
       return nil, err
   }

   var state CoordinationState
   if err := codec.Unmarshal(v, &state); err != nil {
       return nil, err
   }
   return &state, nil
}

func SetCoordinationState(
   ctx context.Context,
   mu state.Mutable,
   state *CoordinationState,
) error {
   k := []byte{coordPrefix}
   v, err := codec.Marshal(state)
   if err != nil {
       return err
   }
   return mu.Insert(ctx, k, v)
}

func UpdateTaskState(
   ctx context.Context,
   mu state.Mutable,
   taskID string,
   status string,
   workers []coordination.WorkerID,
   attestations [][2][]byte,
) error {
   state, err := GetCoordinationState(ctx, mu)
   if err != nil {
       return err
   }

   state.Tasks[taskID] = TaskState{
       Status:       status,
       Workers:      workers,
       Attestations: attestations,
       Timestamp:    string(timestampKey),
   }

   return SetCoordinationState(ctx, mu, state)
}

// Used to serve RPC queries
func GetBalanceFromState(
   ctx context.Context,
   f ReadState,
   addr codec.Address,
) (uint64, error) {
   k := BalanceKey(addr)
   values, errs := f(ctx, [][]byte{k})
   bal, _, err := innerGetBalance(values[0], errs[0])
   return bal, err
}

func innerGetBalance(
   v []byte,
   err error,
) (uint64, bool, error) {
   if errors.Is(err, database.ErrNotFound) {
       return 0, false, nil
   }
   if err != nil {
       return 0, false, err
   }
   val, err := database.ParseUInt64(v)
   if err != nil {
       return 0, false, err
   }
   return val, true, nil
}

func SetBalance(
   ctx context.Context,
   mu state.Mutable,
   addr codec.Address,
   balance uint64,
) error {
   k := BalanceKey(addr)
   return setBalance(ctx, mu, k, balance)
}


// SubBalance example
func SubBalance(
    ctx context.Context,
    st state.Mutable,
    addr codec.Address,
    amount uint64,
) (uint64, error) {
    oldBal, err := GetBalance(ctx, st, addr)
    if err != nil {
        return 0, err
    }
    if oldBal < amount {
        return 0, ErrInsufficientBalance
    }
    newBal := oldBal - amount
    if newBal == 0 {
        // remove key if zero
        return 0, st.Remove(ctx, BalanceKey(addr))
    }
    err = SetBalance(ctx, st, addr, newBal)
    return newBal, err
}

// GetBalance example
func GetBalance(
    ctx context.Context,
    r state.KeyValueReader,
    addr codec.Address,
) (uint64, error) {
    val, err := r.GetValue(ctx, BalanceKey(addr))
    if err != nil {
        // treat “not found” as zero
        return 0, nil
    }
    if len(val) < 8 {
        return 0, fmt.Errorf("corrupt balance data")
    }
    return binary.BigEndian.Uint64(val), nil
}

func HeightKey() (k []byte) {
   return heightKey
}

func TimestampKey() (k []byte) {
   return timestampKey
}

func FeeKey() (k []byte) {
   return feeKey
}

// ShuttleVM Storage Functions

func ObjectKey(id string) []byte {
   k := make([]byte, 1+len(id))
   k[0] = objectPrefix
   copy(k[1:], []byte(id))
   return k
}

func EventKey(timestamp string, id string) []byte {
    k := make([]byte, 1+len(timestamp)+len(id))
    k[0] = eventPrefix
    copy(k[1:], []byte(timestamp))
    copy(k[1+len(timestamp):], []byte(id))
    return k
}

func InputObjectKey() []byte {
   return []byte{inputPrefix}
}

func GetObject(
   ctx context.Context,
   im state.Immutable,
   id string,
) (map[string][]byte, error) {
   k := ObjectKey(id)
   v, err := im.GetValue(ctx, k)
   if errors.Is(err, database.ErrNotFound) {
       return nil, nil
   }
   if err != nil {
       return nil, err
   }

   var obj map[string][]byte
   if err := codec.Unmarshal(v, &obj); err != nil {
       return nil, err
   }
   return obj, nil
}

// Modified object storage to include coordination
func SetObject(
   ctx context.Context,
   mu state.Mutable,
   id string,
   obj map[string][]byte,
   coordState *CoordinationState,
) error {
   k := ObjectKey(id)
   v, err := codec.Marshal(obj)
   if err != nil {
       return err
   }

   // Store object
   if err := mu.Insert(ctx, k, v); err != nil {
       return err
   }

   // Update coordination state if provided
   if coordState != nil {
       if err := SetCoordinationState(ctx, mu, coordState); err != nil {
           return err
       }
   }

   return nil
}

// Modified event queue to include coordination
func QueueEvent(
   ctx context.Context,
   mu state.Mutable,
   id string,
   functionCall string,
   parameters []byte,
   attestations [2]actions.TEEAttestation,
   coordState *CoordinationState,
) error {
   k := EventKey(attestations[0].Timestamp, id)
   event := map[string]interface{}{
       "function_call": functionCall,
       "parameters":    parameters,
       "attestations": attestations,
   }
   v, err := codec.Marshal(event)
   if err != nil {
       return err
   }

   // Store event
   if err := mu.Insert(ctx, k, v); err != nil {
       return err
   }

   // Update coordination state
   if coordState != nil {
       taskID := fmt.Sprintf("event:%s:%s", id, attestations[0].Timestamp)
       workers := []coordination.WorkerID{
           coordination.WorkerID(attestations[0].EnclaveID),
           coordination.WorkerID(attestations[1].EnclaveID),
       }
       attBytes := [][2][]byte{{
           attestations[0].EnclaveID,
           attestations[1].EnclaveID,
       }}
       
       if err := UpdateTaskState(ctx, mu, taskID, "pending", workers, attBytes); err != nil {
           return err
       }
   }

   return nil
}

// Helper function to validate coordination state
func ValidateCoordinationState(state *CoordinationState) error {
   if state == nil {
       return ErrInvalidCoordination
   }
   
   if len(state.Workers) < 2 {
       return fmt.Errorf("%w: insufficient workers", ErrInvalidCoordination)
   }
   
   for taskID, task := range state.Tasks {
       if len(task.Workers) < 2 {
           return fmt.Errorf("%w: insufficient workers for task %s", ErrInvalidCoordination, taskID)
       }
       if len(task.Attestations) == 0 {
           return fmt.Errorf("%w: missing attestations for task %s", ErrInvalidCoordination, taskID)
       }
   }
   
   return nil
}


func GetEvent(
   ctx context.Context,
   im state.Immutable,
   timestamp string,
   id string,
) (map[string]interface{}, error) {
   k := EventKey(timestamp, id)
   v, err := im.GetValue(ctx, k)
   if errors.Is(err, database.ErrNotFound) {
       return nil, nil
   }
   if err != nil {
       return nil, err
   }

   var event map[string]interface{}
   if err := codec.Unmarshal(v, &event); err != nil {
       return nil, err
   }
   return event, nil
}

func GetInputObject(
   ctx context.Context,
   im state.Immutable,
) (string, error) {
   k := InputObjectKey()
   v, err := im.GetValue(ctx, k)
   if errors.Is(err, database.ErrNotFound) {
       return "", nil
   }
   if err != nil {
       return "", err
   }
   return string(v), nil
}

func SetInputObject(
   ctx context.Context,
   mu state.Mutable,
   id string,
) error {
   k := InputObjectKey()
   return mu.Insert(ctx, k, []byte(id))
}

type ObjectState struct {
    Code        []byte    `json:"code"`
    Storage     []byte    `json:"storage"`
    RegionID    string    `json:"region_id"`
    Events      []string  `json:"events"`
    LastUpdated time.Time `json:"last_updated"`
    Status      string    `json:"status"`
}

func RegionKey(id string) []byte {
    k := make([]byte, 1+len(id))
    k[0] = regionPrefix
    copy(k[1:], []byte(id))
    return k
}

func GetRegion(ctx context.Context, im state.Immutable, id string) (map[string]interface{}, error) {
    k := RegionKey(id)
    v, err := im.GetValue(ctx, k)
    if errors.Is(err, database.ErrNotFound) {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }

    var region map[string]interface{}
    if err := codec.Unmarshal(v, &region); err != nil {
        return nil, err
    }
    return region, nil
}

func SetRegion(
   ctx context.Context,
   mu state.Mutable,
   id string,
   region map[string]interface{},
) error {
   k := RegionKey(id)
   v, err := codec.Marshal(region)
   if err != nil {
       return err
   }
   return mu.Insert(ctx, k, v)
}
