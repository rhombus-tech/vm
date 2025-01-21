// regions/pair_manager.go
package regions

import (
    "context"
    "fmt"
    "sync"
    "time"

    "github.com/rhombus-tech/vm/interfaces"
)

type TEEPairManager struct {
    pairs       map[string]*interfaces.TEEPair
    metrics     map[string]*interfaces.TEEPairMetrics
    info        map[string]*interfaces.TEEPairInfo
    coordinator interfaces.Coordinator
    store       interfaces.Storage
    mu          sync.RWMutex
}

func NewTEEPairManager(coordinator interfaces.Coordinator, store interfaces.Storage) *TEEPairManager {
    return &TEEPairManager{
        pairs:       make(map[string]*interfaces.TEEPair),
        metrics:     make(map[string]*interfaces.TEEPairMetrics),
        info:        make(map[string]*interfaces.TEEPairInfo),
        coordinator: coordinator,
        store:       store,
    }
}

func (pm *TEEPairManager) CreatePair(ctx context.Context, config *interfaces.TEEPairConfig) (*interfaces.TEEPairInfo, error) {
    pm.mu.Lock()
    defer pm.mu.Unlock()

    // Create pair info
    info := &interfaces.TEEPairInfo{
        ID:          config.ID,
        SGXEndpoint: config.SGXEndpoint,
        SEVEndpoint: config.SEVEndpoint,
        Status:      "active", // Use constant from interfaces
    }

    // Register workers with coordinator
    sgxWorkerID := fmt.Sprintf("sgx-%s", config.ID)
    sevWorkerID := fmt.Sprintf("sev-%s", config.ID)

    if err := pm.coordinator.RegisterWorker(ctx, sgxWorkerID, []byte("sgx-enclave")); err != nil {
        return nil, fmt.Errorf("failed to register SGX worker: %w", err)
    }

    if err := pm.coordinator.RegisterWorker(ctx, sevWorkerID, []byte("sev-enclave")); err != nil {
        return nil, fmt.Errorf("failed to register SEV worker: %w", err)
    }

    // Initialize metrics
    pm.metrics[config.ID] = &interfaces.TEEPairMetrics{
        PairID:         config.ID,
        LoadFactor:     0.0,
        SuccessRate:    1.0,
        ErrorRate:      0.0,
        TasksProcessed: 0,
        LastUpdate:     time.Now(),
    }

    // Store in persistent storage if available
    if err := pm.store.SaveTEEPairInfo(ctx, info); err != nil {
        return nil, fmt.Errorf("failed to save TEE pair info: %w", err)
    }

    // Store info in memory
    pm.info[config.ID] = info

    return info, nil
}

func (pm *TEEPairManager) GetPair(pairID string) (*interfaces.TEEPair, error) {
    pm.mu.RLock()
    defer pm.mu.RUnlock()

    pair, exists := pm.pairs[pairID]
    if !exists {
        return nil, fmt.Errorf("TEE pair %s not found", pairID)
    }
    return pair, nil
}

func (pm *TEEPairManager) GetPairInfo(pairID string) (*interfaces.TEEPairInfo, error) {
    pm.mu.RLock()
    defer pm.mu.RUnlock()

    info, exists := pm.info[pairID]
    if !exists {
        return nil, fmt.Errorf("TEE pair info %s not found", pairID)
    }
    return info, nil
}

func (pm *TEEPairManager) UpdatePairStatus(ctx context.Context, pairID string, status string) error {
    pm.mu.Lock()
    defer pm.mu.Unlock()

    info, exists := pm.info[pairID]
    if !exists {
        return fmt.Errorf("TEE pair %s not found", pairID)
    }

    info.Status = status
    metrics := pm.metrics[pairID]
    metrics.LastUpdate = time.Now()

    // Update storage
    if err := pm.store.SaveTEEPairInfo(ctx, info); err != nil {
        return fmt.Errorf("failed to save TEE pair status: %w", err)
    }

    return nil
}

func (pm *TEEPairManager) GetMetrics(pairID string) (*interfaces.TEEPairMetrics, error) {
    pm.mu.RLock()
    defer pm.mu.RUnlock()

    metrics, exists := pm.metrics[pairID]
    if !exists {
        return nil, fmt.Errorf("metrics not found for TEE pair %s", pairID)
    }
    return metrics, nil
}

func (pm *TEEPairManager) UpdateMetrics(ctx context.Context, pairID string, updateFn func(*interfaces.TEEPairMetrics)) error {
    pm.mu.Lock()
    defer pm.mu.Unlock()

    metrics, exists := pm.metrics[pairID]
    if !exists {
        return fmt.Errorf("metrics not found for TEE pair %s", pairID)
    }

    updateFn(metrics)
    
    if err := pm.store.SaveTEEMetrics(ctx, pairID, metrics); err != nil {
        return fmt.Errorf("failed to save metrics: %w", err)
    }
    
    return nil
}

func (pm *TEEPairManager) ListPairs() []*interfaces.TEEPairInfo {
    pm.mu.RLock()
    defer pm.mu.RUnlock()

    pairs := make([]*interfaces.TEEPairInfo, 0, len(pm.info))
    for _, info := range pm.info {
        pairs = append(pairs, info)
    }
    return pairs
}

func (pm *TEEPairManager) Close() error {
    pm.mu.Lock()
    defer pm.mu.Unlock()

    // Clean up any resources
    pm.pairs = nil
    pm.metrics = nil
    pm.info = nil

    return nil
}