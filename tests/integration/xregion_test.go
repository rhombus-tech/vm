package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/compute"
	"github.com/rhombus-tech/vm/regions"
	"github.com/rhombus-tech/vm/coordination/xregion"
)

type HealthStatus struct {
    Status         string        `json:"status"`
    LastCheck      time.Time     `json:"last_check"`
    ErrorCount     int           `json:"error_count"`
    SuccessRate    float64       `json:"success_rate"`
    LoadFactor     float64       `json:"load_factor"`
    AverageLatency time.Duration `json:"average_latency"`
}

func TestCrossRegionalTransaction(t *testing.T) {
	// Setup test environment with multiple regions
	vm, err := NewMockVM(&compute.Config{
		MaxTasks: 100,
		RegionConfig: &regions.RegionConfig{
			MaxObjects: 1000,
			MaxEvents: 1000,
		},
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Register regions with mock TEE endpoints
	for _, region := range []string{"region1", "region2", "region3"} {
		err := vm.RegisterRegion(ctx, region, "mock-sgx:"+region, "mock-sev:"+region)
		require.NoError(t, err)
	}

	// Create test objects in different regions
	objectID1 := "test-object-1"

	// Create cross-region intent
	intent := xregion.NewCrossRegionIntent("test-intent", "region1", []string{"region2"})
	intent.AddStateChange("region1", xregion.StateChange{
		Key:       []byte(objectID1),
		Value:     []byte("test-data"),
		Operation: xregion.StateOpTransferOut,
		Source:    "region1",
		Target:    "region2",
	})
	intent.AddStateChange("region2", xregion.StateChange{
		Key:       []byte(objectID1),
		Value:     []byte("test-data"),
		Operation: xregion.StateOpSet,
		Source:    "region1",
		Target:    "region2",
	})

	// Execute cross-regional transaction
	action := &actions.CrossRegionAction{
		Intent: intent,
	}

	result, err := vm.ExecuteInRegion(ctx, "region1", action)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify state consistency across regions
	proof1, err := vm.GetRegionStateProof(ctx, "region1", string(intent.GetStateChanges("region1")[0].Key))
	require.NoError(t, err)
	valid, err := vm.VerifyRegionStateProof(ctx, "region1", proof1)
	require.NoError(t, err)
	require.True(t, valid)

	proof2, err := vm.GetRegionStateProof(ctx, "region2", string(intent.GetStateChanges("region2")[0].Key))
	require.NoError(t, err)
	valid, err = vm.VerifyRegionStateProof(ctx, "region2", proof2)
	require.NoError(t, err)
	require.True(t, valid)
}

func TestCrossRegionalConsistency(t *testing.T) {
	// Setup test environment
	vm, err := NewMockVM(&compute.Config{
		MaxTasks: 100,
		RegionConfig: &regions.RegionConfig{
			MaxObjects: 1000,
			MaxEvents:  1000,
		},
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Register regions
	for _, region := range []string{"region1", "region2"} {
		err := vm.RegisterRegion(ctx, region, "mock-sgx:"+region, "mock-sev:"+region)
		require.NoError(t, err)
	}

	// Create test object in region1
	objectID := "test-object-1"
	createAction := &actions.CreateObjectAction{
		ID:       objectID,
		RegionID: "region1",
		Code:     []byte("test code"),
		Storage:  []byte("test storage"),
	}
	_, err = vm.ExecuteInRegion(ctx, "region1", createAction)
	require.NoError(t, err)

	// Create cross-region intent
	intent := xregion.NewCrossRegionIntent("test-intent", "region1", []string{"region2"})
	intent.AddStateChange("region1", xregion.StateChange{
		Key:       []byte(objectID),
		Value:     []byte("test-data"),
		Operation: xregion.StateOpTransferOut,
		Source:    "region1",
		Target:    "region2",
	})
	intent.AddStateChange("region2", xregion.StateChange{
		Key:       []byte(objectID),
		Value:     []byte("test-data"),
		Operation: xregion.StateOpSet,
		Source:    "region1",
		Target:    "region2",
	})

	// Execute cross-region action
	action := &actions.CrossRegionAction{
		Intent: intent,
	}
	result, err := vm.ExecuteInRegion(ctx, "region1", action)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify state consistency
	proof1, err := vm.GetRegionStateProof(ctx, "region1", objectID)
	require.NoError(t, err)
	valid, err := vm.VerifyRegionStateProof(ctx, "region1", proof1)
	require.NoError(t, err)
	require.True(t, valid)

	proof2, err := vm.GetRegionStateProof(ctx, "region2", objectID)
	require.NoError(t, err)
	valid, err = vm.VerifyRegionStateProof(ctx, "region2", proof2)
	require.NoError(t, err)
	require.True(t, valid)
}

func TestCrossRegionalFailover(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    ctx := context.Background()

    // Create initial state
    createAction := &actions.CreateObjectAction{
        ID:       "failover-test",
        RegionID: regionID,
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }

    result, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
    if err != nil {
        t.Fatalf("failed to create initial state: %v", err)
    }
    if result == nil {
        t.Fatal("initial result is nil")
    }

    // Start health monitoring
    healthChan := monitorRegionHealth(ctx, testVM, regionID)
    
    // Create done channel for cleanup
    done := make(chan struct{})
    defer close(done)

    // Simulate failure after short delay
    go func() {
        time.Sleep(200 * time.Millisecond) // Increased delay before failure
        if err := testVM.SimulateRegionFailure(regionID); err != nil {
            t.Errorf("failed to simulate failure: %v", err)
        }
    }()

    // Collect health updates
    var healthUpdates []*HealthStatus
    timeout := time.After(3 * time.Second) // Increased timeout to ensure we catch recovery
    failureDetected := false
    recoveryDetected := false

collectHealth:
    for {
        select {
        case status := <-healthChan:
            if status == nil {
                break collectHealth
            }
            healthUpdates = append(healthUpdates, status)
            // Break if we detect failure and recovery
            if status.Status == "failed" {
                failureDetected = true
            } else if failureDetected && status.Status == "healthy" {
                recoveryDetected = true
                break collectHealth
            }
        case <-timeout:
            // Check if we detected failure and recovery
            for i, status := range healthUpdates {
                if status.Status == "failed" {
                    failureDetected = true
                } else if failureDetected && i > 0 && status.Status == "healthy" {
                    recoveryDetected = true
                    break
                }
            }
            if !failureDetected {
                t.Fatal("timeout waiting for failure detection")
            }
            break collectHealth
        }
    }

    if !failureDetected {
        t.Error("should have detected failure")
    }
    if !recoveryDetected {
        t.Error("should have detected recovery")
    }
    if len(healthUpdates) == 0 {
        t.Error("should have collected health updates")
    }

    // Verify TEE pair recovery
    verifyTEERecovery(t, healthUpdates)
}

// Add the monitoring function
func monitorRegionHealth(ctx context.Context, vm *MockVM, regionID string) <-chan *HealthStatus {
    statusChan := make(chan *HealthStatus)
    
    go func() {
        defer close(statusChan)
        ticker := time.NewTicker(100 * time.Millisecond)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                status, err := vm.GetRegionHealth(ctx, regionID)
                if err != nil {
                    continue
                }
                select {
                case statusChan <- status:
                case <-ctx.Done():
                    return
                }
            }
        }
    }()

    return statusChan
}