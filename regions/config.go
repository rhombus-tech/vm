// regions/config.go
package regions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidConfig    = errors.New("invalid region configuration")
	ErrInvalidTEEPair   = errors.New("invalid TEE pair")
	ErrDuplicateTEEPair = errors.New("duplicate TEE pair")
	ErrTEEMismatch      = errors.New("TEE ID type mismatch")
	ErrIDRequired       = errors.New("region ID required")
	ErrInvalidLimits    = errors.New("invalid resource limits")
	ErrConfigNotFound   = errors.New("region configuration not found")
	ErrInvalidLocation  = errors.New("invalid geographic location")
    ErrInvalidDistance  = errors.New("invalid maximum distance")
)

// LoadBalancerConfig defines load balancing settings for a region
type LoadBalancerConfig struct {
	MaxLoadFactor     float64 // Maximum load factor (0.0-1.0)
	MaxLatencyMs      float64 // Maximum latency in milliseconds
	MaxErrorRate      float64 // Maximum error rate threshold
	MinActiveWorkers  int     // Minimum number of active workers
	MaxPendingTasks   int     // Maximum number of pending tasks
	HealthCheckWindow int64   // Health check window in seconds
	GeoPreference     bool    // Whether to prefer geographically closer regions
    MaxDistance       float64 // Maximum acceptable distance in km
}

type TEEPair struct {
    ID          string `json:"id"`
    SGXID       []byte `json:"sgx_id"`
    SEVID       []byte `json:"sev_id"`
    SGXEndpoint string `json:"sgx_endpoint"`
    SEVEndpoint string `json:"sev_endpoint"`
    Status      string `json:"status"`
}


// RegionConfig defines the configuration for a region
type RegionConfig struct {
	ID           string             // Unique region identifier
	TEEPairs     []TEEPair          // List of SGX+SEV pairs
	MaxObjects   int                // Maximum number of objects in region
	MaxEvents    int                // Maximum number of events in region
	LoadBalancer LoadBalancerConfig // Load balancer settings
	Location     GeoLocation		// Region location
}

type TEEPairConfig struct {
    // Identity and Endpoints
    ID           string `json:"id"`
    SGXEndpoint  string `json:"sgx_endpoint"`
    SEVEndpoint  string `json:"sev_endpoint"`
    
    // Capacity and Priority
    Capacity     uint64 `json:"capacity"`      // Changed to uint64 for consistency
    Priority     int    `json:"priority"`
    
    // Operational Parameters
    Thresholds struct {
        MinSuccessRate float64       `json:"min_success_rate"`
        MaxErrorRate   float64       `json:"max_error_rate"`
        MaxLatency     time.Duration `json:"max_latency"`
        MaxLoadFactor  float64       `json:"max_load_factor"`
    } `json:"thresholds"`
    
    // Health Check Configuration
    HealthCheck struct {
        Interval     time.Duration `json:"interval"`
        MaxFailures  uint64        `json:"max_failures"`
        MinHealthy   uint64        `json:"min_healthy"`
    } `json:"health_check"`

    // Resource Limits
    ResourceLimits struct {
        MaxCPUUsage    float64 `json:"max_cpu_usage"`    // percentage (0-1)
        MaxMemoryUsage float64 `json:"max_memory_usage"` // percentage (0-1)
        MaxTaskQueue   uint64  `json:"max_task_queue"`
    } `json:"resource_limits"`

    // Attestation Settings
    Attestation struct {
        RequireRenewal bool          `json:"require_renewal"`
        RenewalPeriod  time.Duration `json:"renewal_period"`
        MaxAge         time.Duration `json:"max_age"`
    } `json:"attestation"`
}

func NewTEEPairConfig(id, sgxEndpoint, sevEndpoint string) *TEEPairConfig {
    config := &TEEPairConfig{
        ID:          id,
        SGXEndpoint: sgxEndpoint,
        SEVEndpoint: sevEndpoint,
        Capacity:    100,  // Default capacity
        Priority:    1,    // Default priority
    }

    // Set default thresholds
    config.Thresholds.MinSuccessRate = 0.95  // 95%
    config.Thresholds.MaxErrorRate = 0.05    // 5%
    config.Thresholds.MaxLatency = 5 * time.Second
    config.Thresholds.MaxLoadFactor = 0.8    // 80%

    // Set default health check parameters
    config.HealthCheck.Interval = 30 * time.Second
    config.HealthCheck.MaxFailures = 3
    config.HealthCheck.MinHealthy = 1

    // Set default resource limits
    config.ResourceLimits.MaxCPUUsage = 0.8    // 80%
    config.ResourceLimits.MaxMemoryUsage = 0.8 // 80%
    config.ResourceLimits.MaxTaskQueue = 1000

    // Set default attestation settings
    config.Attestation.RequireRenewal = true
    config.Attestation.RenewalPeriod = 1 * time.Hour
    config.Attestation.MaxAge = 24 * time.Hour

    return config
}

func (c *TEEPairConfig) Validate() error {
    if c.ID == "" {
        return fmt.Errorf("ID is required")
    }
    if c.SGXEndpoint == "" {
        return fmt.Errorf("SGX endpoint is required")
    }
    if c.SEVEndpoint == "" {
        return fmt.Errorf("SEV endpoint is required")
    }
    if c.Capacity <= 0 {
        return fmt.Errorf("capacity must be greater than 0")
    }

    // Validate thresholds
    if c.Thresholds.MinSuccessRate <= 0 || c.Thresholds.MinSuccessRate > 1 {
        return fmt.Errorf("invalid min success rate: %v", c.Thresholds.MinSuccessRate)
    }
    if c.Thresholds.MaxErrorRate < 0 || c.Thresholds.MaxErrorRate >= 1 {
        return fmt.Errorf("invalid max error rate: %v", c.Thresholds.MaxErrorRate)
    }
    if c.Thresholds.MaxLatency <= 0 {
        return fmt.Errorf("invalid max latency: %v", c.Thresholds.MaxLatency)
    }

    // Validate health check settings
    if c.HealthCheck.Interval <= 0 {
        return fmt.Errorf("invalid health check interval: %v", c.HealthCheck.Interval)
    }

    // Validate resource limits
    if c.ResourceLimits.MaxCPUUsage <= 0 || c.ResourceLimits.MaxCPUUsage > 1 {
        return fmt.Errorf("invalid max CPU usage: %v", c.ResourceLimits.MaxCPUUsage)
    }
    if c.ResourceLimits.MaxMemoryUsage <= 0 || c.ResourceLimits.MaxMemoryUsage > 1 {
        return fmt.Errorf("invalid max memory usage: %v", c.ResourceLimits.MaxMemoryUsage)
    }

    // Validate attestation settings
    if c.Attestation.RequireRenewal {
        if c.Attestation.RenewalPeriod <= 0 {
            return fmt.Errorf("invalid renewal period: %v", c.Attestation.RenewalPeriod)
        }
        if c.Attestation.MaxAge <= c.Attestation.RenewalPeriod {
            return fmt.Errorf("max age must be greater than renewal period")
        }
    }

    return nil
}

func (c *TEEPairConfig) IsAtCapacity(currentTasks uint64) bool {
    return currentTasks >= c.Capacity
}

func (c *TEEPairConfig) GetEffectivePriority(loadFactor float64) int {
    // Reduce priority as load increases
    if loadFactor > c.Thresholds.MaxLoadFactor {
        return c.Priority - 1
    }
    return c.Priority
}


func DefaultConfig() *BalancerConfig {
    return &BalancerConfig{
        MaxLoadFactor:     0.8,
        MaxLatencyMs:      1000,
        MaxErrorRate:      0.1,
        MinSuccessRate:    0.95,
        MinActiveWorkers:  2,
        MaxPendingTasks:   1000,
        HealthCheckWindow: 5 * time.Minute,
        GeoPreference:     true,
        MaxDistance:       5000, // 5000km
    }
}


// Validate checks if a region configuration is valid
func (c *RegionConfig) Validate() error {
    // Existing validations
    if c.ID == "" {
        return ErrIDRequired
    }

    if len(c.TEEPairs) == 0 {
        return fmt.Errorf("%w: at least one TEE pair required", ErrInvalidConfig)
    }

    // Check for duplicate TEE pairs
    seen := make(map[string]bool)
    for i, pair := range c.TEEPairs {
        if len(pair.SGXID) == 0 {
            return fmt.Errorf("%w: missing SGX ID in pair %d", ErrInvalidTEEPair, i)
        }

        if len(pair.SEVID) == 0 {
            return fmt.Errorf("%w: missing SEV ID in pair %d", ErrInvalidTEEPair, i)
        }

        key := fmt.Sprintf("%x:%x", pair.SGXID, pair.SEVID)
        if seen[key] {
            return fmt.Errorf("%w: pair %d", ErrDuplicateTEEPair, i)
        }
        seen[key] = true
    }

    // Validate resource limits
    if c.MaxObjects <= 0 || c.MaxEvents <= 0 {
        return ErrInvalidLimits
    }

    // Validate load balancer config
    if err := c.validateLoadBalancerConfig(); err != nil {
        return err
    }

    // Add location validation
	if err := c.validateLocation(); err != nil {
        return err
    }

    return nil

	// Validate resource limits
	if c.MaxObjects <= 0 || c.MaxEvents <= 0 {
        return ErrInvalidLimits
    }

	// Validate load balancer config
	if err := c.validateLoadBalancerConfig(); err != nil {
		return err
	}

	return nil
}

func (c *RegionConfig) validateLocation() error {
    if c.Location.Latitude < -90 || c.Location.Latitude > 90 {
        return fmt.Errorf("%w: invalid latitude", ErrInvalidLocation)
    }
    if c.Location.Longitude < -180 || c.Location.Longitude > 180 {
        return fmt.Errorf("%w: invalid longitude", ErrInvalidLocation)
    }
    if c.Location.DataCenter == "" {
        return fmt.Errorf("%w: missing datacenter", ErrInvalidLocation)
    }
    if c.Location.Region == "" {
        return fmt.Errorf("%w: missing region name", ErrInvalidLocation)
    }
    return nil
}


// validateLoadBalancerConfig validates load balancer settings
func (c *RegionConfig) validateLoadBalancerConfig() error {
    lb := c.LoadBalancer

    // Existing validations
    if lb.MaxLoadFactor <= 0 || lb.MaxLoadFactor > 1.0 {
        return fmt.Errorf("%w: invalid load factor", ErrInvalidConfig)
    }
    if lb.MaxLatencyMs <= 0 {
        return fmt.Errorf("%w: invalid max latency", ErrInvalidConfig)
    }
    if lb.MaxErrorRate < 0 || lb.MaxErrorRate > 1.0 {
        return fmt.Errorf("%w: invalid error rate", ErrInvalidConfig)
    }
    if lb.MinActiveWorkers < 1 {
        return fmt.Errorf("%w: invalid min workers", ErrInvalidConfig)
    }
    if lb.MaxPendingTasks < 1 {
        return fmt.Errorf("%w: invalid max pending tasks", ErrInvalidConfig)
    }
    if lb.HealthCheckWindow <= 0 {
        return fmt.Errorf("%w: invalid health check window", ErrInvalidConfig)
    }

    // Add new validations
    if lb.GeoPreference && lb.MaxDistance <= 0 {
        return fmt.Errorf("%w: invalid max distance", ErrInvalidDistance)
    }

    return nil
}

// UpdateConfig validates and updates a region configuration
func (m *RegionManager) UpdateConfig(regionID string, config *RegionConfig) error {
	// Validate config
	if err := config.Validate(); err != nil {
		return err
	}

	// Check that region IDs match
	if config.ID != regionID {
		return fmt.Errorf("%w: ID mismatch", ErrInvalidConfig)
	}

	// Store config
	if err := m.store.SetRegionConfig(regionID, config); err != nil {
		return fmt.Errorf("failed to store config: %w", err)
	}

	// Update in-memory cache
	m.configs[regionID] = config

	return nil
}

// GetConfig retrieves a region configuration
func (m *RegionManager) GetConfig(regionID string) (*RegionConfig, error) {
	// Check in-memory cache first
	if config, exists := m.configs[regionID]; exists {
		return config, nil
	}

	// Load from storage
	config, err := m.store.GetRegionConfig(regionID)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	if config == nil {
		return nil, ErrConfigNotFound
	}

	// Cache config
	m.configs[regionID] = config

	return config, nil
}

// DeleteConfig removes a region configuration
func (m *RegionManager) DeleteRegion(ctx context.Context, regionID string) error {
    err := m.store.DeleteRegionConfig(ctx, regionID)
    if err != nil {
        return fmt.Errorf("failed to delete region config: %w", err)
    }
    return nil
}


// FindTEEPair looks up a TEE pair by IDs
func (m *RegionManager) FindTEEPair(regionID string, sgxID, sevID []byte) (*TEEPair, error) {
	config, err := m.GetConfig(regionID)
	if err != nil {
		return nil, err
	}

	for _, pair := range config.TEEPairs {
		if bytes.Equal(pair.SGXID, sgxID) && bytes.Equal(pair.SEVID, sevID) {
			return &pair, nil
		}
	}

	return nil, ErrInvalidTEEPair
}

// ValidateTEEPair validates a TEE pair against a region's configuration
func (m *RegionManager) ValidateTEEPair(regionID string, sgxID, sevID []byte) error {
	_, err := m.FindTEEPair(regionID, sgxID, sevID)
	return err
}

func (rm *RegionManager) AddTEEPair(regionID string, pair *TEEPair) error {
    rm.cacheLock.Lock()
    defer rm.cacheLock.Unlock()

    config, exists := rm.cache[regionID]
    if !exists {
        return fmt.Errorf("region %s not found", regionID)
    }

    config.TEEPairs = append(config.TEEPairs, *pair)
    return rm.store.SetRegionConfig(regionID, config)
}

func (rm *RegionManager) GetTEEPairs(regionID string) ([]TEEPair, error) {
    rm.cacheLock.RLock()
    defer rm.cacheLock.RUnlock()

    config, exists := rm.cache[regionID]
    if !exists {
        return nil, fmt.Errorf("region %s not found", regionID)
    }

    return config.TEEPairs, nil
}

func (rm *RegionManager) GetRegionConfig(regionID string) (*RegionConfig, error) {
    rm.cacheLock.RLock()  // Using cacheLock
    defer rm.cacheLock.RUnlock()
    
    config, exists := rm.cache[regionID]
    if exists {
        return config, nil
    }

    // If not in cache, try to load from store
    rm.cacheLock.Lock()
    defer rm.cacheLock.Unlock()

    // Double check after acquiring write lock
    if config, exists = rm.cache[regionID]; exists {
        return config, nil
    }

    // Load from store
    config, err := rm.store.GetRegionConfig(regionID)
    if err != nil {
        return nil, fmt.Errorf("failed to get region config: %w", err)
    }

    // Cache the config
    rm.cache[regionID] = config
    return config, nil
}

func (rm *RegionManager) GetBalancer() *RegionBalancer {
    return rm.balancer
}

func (rm *RegionManager) UpdateCache(regionID string, config *RegionConfig) {
    rm.cacheLock.Lock()
    defer rm.cacheLock.Unlock()
    rm.cache[regionID] = config
}

func (rm *RegionManager) ClearCache() {
    rm.cacheLock.Lock()
    defer rm.cacheLock.Unlock()
    rm.cache = make(map[string]*RegionConfig)
}

func (rm *RegionManager) InvalidateCache(regionID string) {
    rm.cacheLock.Lock()
    defer rm.cacheLock.Unlock()
    delete(rm.cache, regionID)
}

// ListRegions returns a list of all configured region IDs
func (rm *RegionManager) ListRegions() []string {
    rm.cacheLock.RLock()
    defer rm.cacheLock.RUnlock()
    
    regions := make([]string, 0, len(rm.configs))
    for regionID := range rm.configs {
        regions = append(regions, regionID)
    }
    return regions
}