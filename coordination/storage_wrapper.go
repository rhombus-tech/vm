// coordination/storage_wrapper.go
package coordination

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"
    
    "github.com/ava-labs/avalanchego/x/merkledb"
)

const (
    channelPrefix = "channel/"
    workerPrefix  = "worker/"
    regionPrefix  = "region/"
    teePrefix     = "tee/"
    metricsPrefix = "metrics/"
    stateKey      = "coordinator_state"
)

// StorageWrapper adapts a BaseStorage to implement full Storage interface
type StorageWrapper struct {
    base BaseStorage
}

// Constructor
func NewStorageWrapper(base BaseStorage) *StorageWrapper {
    return &StorageWrapper{
        base: base,
    }
}

// Basic operations
func (sw *StorageWrapper) Put(ctx context.Context, key []byte, value []byte) error {
    return sw.base.Put(ctx, key, value)
}

func (sw *StorageWrapper) Get(ctx context.Context, key []byte) ([]byte, error) {
    return sw.base.Get(ctx, key)
}

func (sw *StorageWrapper) Delete(ctx context.Context, key []byte) error {
    return sw.base.Delete(ctx, key)  // Changed from sw.db to sw.base
}



// Channel operations
func (sw *StorageWrapper) SaveChannel(ctx context.Context, channel *SecureChannel) error {
    key := makeChannelKey(channel.Worker1, channel.Worker2)
    data, err := json.Marshal(channel)
    if err != nil {
        return fmt.Errorf("failed to marshal channel: %w", err)
    }
    return sw.Put(ctx, []byte(channelPrefix+key), data)
}

func (sw *StorageWrapper) LoadChannel(ctx context.Context, worker1, worker2 WorkerID) (*SecureChannel, error) {
    key := makeChannelKey(worker1, worker2)
    data, err := sw.Get(ctx, []byte(channelPrefix+key))
    if err != nil {
        return nil, err
    }
    
    var channel SecureChannel
    if err := json.Unmarshal(data, &channel); err != nil {
        return nil, fmt.Errorf("failed to unmarshal channel: %w", err)
    }
    
    // Reinitialize channels after unmarshaling
    channel.messages = make(chan []byte, 100)
    channel.done = make(chan struct{})
    
    return &channel, nil
}

func (sw *StorageWrapper) DeleteChannel(ctx context.Context, worker1, worker2 WorkerID) error {
    key := makeChannelKey(worker1, worker2)
    return sw.Delete(ctx, []byte(channelPrefix+key))
}

// Worker operations
func (sw *StorageWrapper) SaveWorker(ctx context.Context, worker *Worker) error {
    data, err := json.Marshal(worker)
    if err != nil {
        return fmt.Errorf("failed to marshal worker: %w", err)
    }
    return sw.Put(ctx, []byte(workerPrefix+string(worker.ID)), data)
}

func (sw *StorageWrapper) LoadWorker(ctx context.Context, id WorkerID) (*Worker, error) {
    data, err := sw.Get(ctx, []byte(workerPrefix+string(id)))
    if err != nil {
        return nil, err
    }
    
    var worker Worker
    if err := json.Unmarshal(data, &worker); err != nil {
        return nil, fmt.Errorf("failed to unmarshal worker: %w", err)
    }
    
    // Reinitialize channels
    worker.msgCh = make(chan *Message, 100)
    worker.doneCh = make(chan struct{})
    
    return &worker, nil
}

func (sw *StorageWrapper) DeleteWorker(ctx context.Context, id WorkerID) error {
    return sw.Delete(ctx, []byte(workerPrefix+string(id)))
}

// Region operations
func (sw *StorageWrapper) SaveRegion(ctx context.Context, region *Region) error {
    data, err := json.Marshal(region)
    if err != nil {
        return fmt.Errorf("failed to marshal region: %w", err)
    }
    return sw.Put(ctx, []byte(regionPrefix+region.ID), data)
}

func (sw *StorageWrapper) LoadRegion(ctx context.Context, id string) (*Region, error) {
    data, err := sw.Get(ctx, []byte(regionPrefix+id))
    if err != nil {
        return nil, err
    }
    
    var region Region
    if err := json.Unmarshal(data, &region); err != nil {
        return nil, fmt.Errorf("failed to unmarshal region: %w", err)
    }
    return &region, nil
}

func (sw *StorageWrapper) DeleteRegion(ctx context.Context, id string) error {
    return sw.Delete(ctx, []byte(regionPrefix+id))
}

// Coordinator state operations
func (sw *StorageWrapper) SaveCoordinatorState(ctx context.Context, state map[string]interface{}) error {
    data, err := json.Marshal(state)
    if err != nil {
        return fmt.Errorf("failed to marshal coordinator state: %w", err)
    }
    return sw.Put(ctx, []byte(stateKey), data)
}

func (sw *StorageWrapper) LoadCoordinatorState(ctx context.Context) (map[string]interface{}, error) {
    data, err := sw.Get(ctx, []byte(stateKey))
    if err != nil {
        return nil, err
    }
    
    var state map[string]interface{}
    if err := json.Unmarshal(data, &state); err != nil {
        return nil, fmt.Errorf("failed to unmarshal coordinator state: %w", err)
    }
    return state, nil
}

// TEE operations
func (sw *StorageWrapper) SaveTEEPairInfo(ctx context.Context, info *TEEPairInfo) error {
    data, err := json.Marshal(info)
    if err != nil {
        return fmt.Errorf("failed to marshal TEE pair info: %w", err)
    }
    return sw.Put(ctx, []byte(teePrefix+info.ID), data)
}

func (sw *StorageWrapper) GetTEEPairInfo(ctx context.Context, pairID string) (*TEEPairInfo, error) {
    data, err := sw.Get(ctx, []byte(teePrefix+pairID))
    if err != nil {
        return nil, err
    }
    
    var info TEEPairInfo
    if err := json.Unmarshal(data, &info); err != nil {
        return nil, fmt.Errorf("failed to unmarshal TEE pair info: %w", err)
    }
    return &info, nil
}

func (sw *StorageWrapper) ListTEEPairInfo(ctx context.Context) ([]*TEEPairInfo, error) {
    pairs := make([]*TEEPairInfo, 0)
    prefix := []byte(teePrefix)
    
    // Assuming base storage supports prefix iteration
    iterator, err := sw.base.GetByPrefix(ctx, prefix)
    if err != nil {
        return nil, err
    }
    
    for _, value := range iterator {
        var info TEEPairInfo
        if err := json.Unmarshal(value, &info); err != nil {
            return nil, fmt.Errorf("failed to unmarshal TEE pair info: %w", err)
        }
        pairs = append(pairs, &info)
    }
    
    return pairs, nil
}

// Metrics operations
func (sw *StorageWrapper) SaveTEEMetrics(ctx context.Context, pairID string, metrics *TEEPairMetrics) error {
    data, err := json.Marshal(metrics)
    if err != nil {
        return fmt.Errorf("failed to marshal TEE metrics: %w", err)
    }
    return sw.Put(ctx, []byte(metricsPrefix+pairID), data)
}

func (sw *StorageWrapper) GetTEEMetrics(ctx context.Context, pairID string) (*TEEPairMetrics, error) {
    data, err := sw.Get(ctx, []byte(metricsPrefix+pairID))
    if err != nil {
        return nil, err
    }
    
    var metrics TEEPairMetrics
    if err := json.Unmarshal(data, &metrics); err != nil {
        return nil, fmt.Errorf("failed to unmarshal TEE metrics: %w", err)
    }
    return &metrics, nil
}

// Helper functions
func makeChannelKey(worker1, worker2 WorkerID) string {
    if strings.Compare(string(worker1), string(worker2)) < 0 {
        return string(worker1) + ":" + string(worker2)
    }
    return string(worker2) + ":" + string(worker1)
}

// View operations
func (sw *StorageWrapper) NewView(ctx context.Context, changes merkledb.ViewChanges) error {
    // Optional: implement if base storage supports views
    return nil
}

func (sw *StorageWrapper) Close() error {
    // Optional: implement if base storage needs cleanup
    return nil
}