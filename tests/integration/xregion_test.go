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
	for _, region := range []string{"region1", "region2", "region3"} {
		err := vm.RegisterRegion(ctx, region, "mock-sgx:"+region, "mock-sev:"+region)
		require.NoError(t, err)
	}

	// Start health monitoring
	done := make(chan struct{})
	healthChan := monitorTEEHealth(ctx, vm, "region1")

	// Create cross-region intent
	intent := xregion.NewCrossRegionIntent("test-intent", "region1", []string{"region2"})
	intent.AddStateChange("region1", xregion.StateChange{
		Key:       []byte("test-object"),
		Value:     []byte("test-data"),
		Operation: xregion.StateOpTransferOut,
		Source:    "region1",
		Target:    "region2",
	})
	intent.AddStateChange("region2", xregion.StateChange{
		Key:       []byte("test-object"),
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

	// Simulate region failure after transaction
	go func() {
		time.Sleep(100 * time.Millisecond)
		err := vm.SimulateRegionFailure("region1")
		require.NoError(t, err)
		close(done)
	}()

	// Wait for failure simulation
	<-done

	// Collect health status updates with longer timeout
	var healthUpdates []*HealthStatus
	timeout := time.After(2 * time.Second)
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

	require.True(t, failureDetected, "Should have detected failure")
	require.True(t, recoveryDetected, "Should have detected recovery")
	require.NotEmpty(t, healthUpdates, "Should have collected health updates")

	// Verify TEE pair recovery
	verifyTEERecovery(t, healthUpdates)
}
