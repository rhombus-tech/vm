// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package vm

import (
	"fmt"

	"github.com/rhombus-tech/vm/compute"
)

// Config represents the configuration for the VM, including regional settings
type Config struct {
    NetworkID uint32 `json:"network_id"`
    ChainID   string `json:"chain_id"`
    
    ComputeNodeEndpoints map[string]string   `json:"compute_node_endpoints"`
    VerificationOnly     bool               `json:"verification_only"`
    Regions             []RegionConfig      `json:"regions"`
    MaxCodeSize uint64 `json:"max_code_size"`
}

func DefaultConfig() *Config {
    return &Config{
        NetworkID: 0,
        ChainID: "",
        MaxCodeSize: 1024 * 1024, // 1MB default
        ComputeNodeEndpoints: make(map[string]compute.NodeClientConfig),
        Regions: []RegionConfig{},
        VerificationOnly:     false,
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