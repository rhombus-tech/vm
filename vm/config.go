// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package vm

import (
	"fmt"
	"time"

	"github.com/rhombus-tech/vm/compute"
    "github.com/rhombus-tech/vm/regions"
)

type TEEConfig struct {
    Pairs              map[string][]regions.TEEPair `json:"tee_pairs"`
    HealthCheckInterval time.Duration              `json:"health_check_interval"`
    MetricsInterval    time.Duration              `json:"metrics_interval"`
    RetryAttempts      int                        `json:"retry_attempts"`
    RetryDelay         time.Duration              `json:"retry_delay"`
}


// Config represents the configuration for the VM, including regional settings
type Config struct {
    NetworkID           uint32                              `json:"network_id"`
    ChainID            string                              `json:"chain_id"`
    MaxCodeSize        uint64                              `json:"max_code_size"`
    ComputeNodeEndpoints map[string]compute.NodeClientConfig `json:"compute_node_endpoints"`
    ControllerPath     string                              `json:"controller_path"`
    WasmPath          string                              `json:"wasm_path"`
    Regions           []RegionConfig                      `json:"regions"`
    VerificationOnly  bool                                `json:"verification_only"`
    TEEConfig         *regions.TEEPairConfig              `json:"tee_config"` // Use regions.TEEPairConfig
}


func DefaultConfig() *Config {
    return &Config{
        NetworkID: 0,
        ChainID: "",
        MaxCodeSize: 1024 * 1024, // 1MB default
        ComputeNodeEndpoints: make(map[string]compute.NodeClientConfig),
        ControllerPath: "/usr/local/bin/tee-controller",
        WasmPath: "/usr/local/bin/tee-wasm-module.wasm",
        Regions: []RegionConfig{},
        VerificationOnly: false,
        TEEConfig: &regions.TEEPairConfig{ // Use regions.TEEPairConfig
            ID:          "default",
            SGXEndpoint: "/default/sgx-endpoint",
            SEVEndpoint: "/default/sev-endpoint",
            Capacity:    100,
            Priority:    1,
        },
    }
}

type TEEPairConfig struct {
    // Identity and Endpoints
    ID           string `json:"id"`
    SGXEndpoint  string `json:"sgx_endpoint"`
    SEVEndpoint  string `json:"sev_endpoint"`
    
    // Capacity and Priority
    Capacity     uint64 `json:"capacity"`
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
        MaxCPUUsage    float64 `json:"max_cpu_usage"`
        MaxMemoryUsage float64 `json:"max_memory_usage"`
        MaxTaskQueue   uint64  `json:"max_task_queue"`
    } `json:"resource_limits"`

    // Attestation Settings
    Attestation struct {
        RequireRenewal bool          `json:"require_renewal"`
        RenewalPeriod  time.Duration `json:"renewal_period"`
        MaxAge         time.Duration `json:"max_age"`
    } `json:"attestation"`
}


func DefaultTEEPairConfig() *TEEPairConfig {
    return &TEEPairConfig{
        // Identity and Endpoints
        ID:          "default",
        SGXEndpoint: "localhost:50051",
        SEVEndpoint: "localhost:50052",
        
        // Capacity and Priority
        Capacity:    100,
        Priority:    1,
        
        // Operational Parameters
        Thresholds: struct {
            MinSuccessRate float64       `json:"min_success_rate"`
            MaxErrorRate   float64       `json:"max_error_rate"`
            MaxLatency     time.Duration `json:"max_latency"`
            MaxLoadFactor  float64       `json:"max_load_factor"`
        }{
            MinSuccessRate: 0.95,  // Converting your 95% success rate
            MaxErrorRate:   0.1,   // Converting your 10% error threshold
            MaxLatency:     time.Second,
            MaxLoadFactor:  0.8,   // Converting your 80% load factor
        },
        
        // Health Check Configuration
        HealthCheck: struct {
            Interval     time.Duration `json:"interval"`
            MaxFailures  uint64        `json:"max_failures"`
            MinHealthy   uint64        `json:"min_healthy"`
        }{
            Interval:    30 * time.Second,  // Converting your HealthCheckInterval
            MaxFailures: 3,                 // Converting your MaxRetries
            MinHealthy:  1,                 // Converting your MinHealthyPairs
        },
        
        // Resource Limits
        ResourceLimits: struct {
            MaxCPUUsage    float64 `json:"max_cpu_usage"`
            MaxMemoryUsage float64 `json:"max_memory_usage"`
            MaxTaskQueue   uint64  `json:"max_task_queue"`
        }{
            MaxCPUUsage:    0.8,  // 80%
            MaxMemoryUsage: 0.8,  // 80%
            MaxTaskQueue:   1000,
        },
        
        // Attestation Settings
        Attestation: struct {
            RequireRenewal bool          `json:"require_renewal"`
            RenewalPeriod  time.Duration `json:"renewal_period"`
            MaxAge         time.Duration `json:"max_age"`
        }{
            RequireRenewal: true,
            RenewalPeriod:  10 * time.Second,  // Converting your AttestationTimeout
            MaxAge:         24 * time.Hour,
        },
    }
}


// RegionConfig represents the configuration for a single region
type RegionConfig struct {
    ID          string `json:"id"`
    SGXEndpoint string `json:"sgx_endpoint"`
    SEVEndpoint string `json:"sev_endpoint"`
}

// RegionClient handles TEE interactions for a specific region
type RegionClient struct {
    config RegionConfig
    sgx    *TEEClient
    sev    *TEEClient
}

// NewRegionClient creates a new client for regional TEE interactions
func NewRegionClient(rc RegionConfig) (*RegionClient, error) {
    sgx, err := NewTEEClient(rc.SGXEndpoint, TEETypeSGX)
    if err != nil {
        return nil, fmt.Errorf("failed to create SGX client: %w", err)
    }

    sev, err := NewTEEClient(rc.SEVEndpoint, TEETypeSEV)
    if err != nil {
        sgx.Close() // Clean up SGX client if SEV fails
        return nil, fmt.Errorf("failed to create SEV client: %w", err)
    }

    return &RegionClient{
        config: rc,
        sgx:    sgx,
        sev:    sev,
    }, nil
}

// Close cleans up region client resources
func (rc *RegionClient) Close() error {
    var errs []error
    if err := rc.sgx.Close(); err != nil {
        errs = append(errs, fmt.Errorf("close SGX client: %w", err))
    }
    if err := rc.sev.Close(); err != nil {
        errs = append(errs, fmt.Errorf("close SEV client: %w", err))
    }
    if len(errs) > 0 {
        return fmt.Errorf("close region client errors: %v", errs)
    }
    return nil
}