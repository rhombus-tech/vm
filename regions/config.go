// regions/config.go
package regions

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/ava-labs/avalanchego/x/merkledb"
)

var (
	ErrInvalidConfig    = errors.New("invalid region configuration")
	ErrInvalidTEEPair   = errors.New("invalid TEE pair")
	ErrDuplicateTEEPair = errors.New("duplicate TEE pair")
	ErrTEEMismatch      = errors.New("TEE ID type mismatch")
	ErrIDRequired       = errors.New("region ID required")
	ErrInvalidLimits    = errors.New("invalid resource limits")
	ErrConfigNotFound   = errors.New("region configuration not found")
)

// LoadBalancerConfig defines load balancing settings for a region
type LoadBalancerConfig struct {
	MaxLoadFactor     float64 // Maximum load factor (0.0-1.0)
	MaxLatencyMs      float64 // Maximum latency in milliseconds
	MaxErrorRate      float64 // Maximum error rate threshold
	MinActiveWorkers  int     // Minimum number of active workers
	MaxPendingTasks   int     // Maximum number of pending tasks
	HealthCheckWindow int64   // Health check window in seconds
}

// TEEPair represents a pair of TEE IDs (SGX + SEV)
type TEEPair struct {
	SGXID []byte // SGX enclave ID
	SEVID []byte // SEV enclave ID
}

// RegionConfig defines the configuration for a region
type RegionConfig struct {
	ID           string             // Unique region identifier
	TEEPairs     []TEEPair          // List of SGX+SEV pairs
	MaxObjects   int                // Maximum number of objects in region
	MaxEvents    int                // Maximum number of events in region
	LoadBalancer LoadBalancerConfig // Load balancer settings
}

// RegionManager handles region configuration management
// Storage interface defines methods required for region configuration persistence
type Storage interface {
	// SetRegionConfig stores a region configuration
	SetRegionConfig(regionID string, config *RegionConfig) error

	// GetRegionConfig retrieves a region configuration
	GetRegionConfig(regionID string) (*RegionConfig, error)

	// DeleteRegionConfig removes a region configuration
	DeleteRegionConfig(ctx context.Context, regionID string) error

	GetRegionProof(ctx context.Context, regionID string) (*merkledb.Proof, error)
}

type RegionManager struct {
	configs map[string]*RegionConfig
	store   Storage
}

// NewRegionManager creates a new region manager instance
func NewRegionManager(store Storage) *RegionManager {
	return &RegionManager{
		configs: make(map[string]*RegionConfig),
		store:   store,
	}
}

// Validate checks if a region configuration is valid
func (c *RegionConfig) Validate() error {
	if c.ID == "" {
		return ErrIDRequired
	}

	if len(c.TEEPairs) == 0 {
		return fmt.Errorf("%w: at least one TEE pair required", ErrInvalidConfig)
	}

	// Check for duplicate TEE pairs
	seen := make(map[string]bool)
	for i, pair := range c.TEEPairs {
		// Validate SGX ID
		if len(pair.SGXID) == 0 {
			return fmt.Errorf("%w: missing SGX ID in pair %d", ErrInvalidTEEPair, i)
		}

		// Validate SEV ID
		if len(pair.SEVID) == 0 {
			return fmt.Errorf("%w: missing SEV ID in pair %d", ErrInvalidTEEPair, i)
		}

		// Check for duplicates using string representation
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

	return nil
}

// validateLoadBalancerConfig validates load balancer settings
func (c *RegionConfig) validateLoadBalancerConfig() error {
	lb := c.LoadBalancer

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

// ListRegions returns a list of all configured region IDs
func (m *RegionManager) ListRegions() []string {
	regions := make([]string, 0, len(m.configs))
	for regionID := range m.configs {
		regions = append(regions, regionID)
	}
	return regions
}
