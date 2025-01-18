// tests/vm/utils_test.go

package vm_test

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

// Helper function to wait for operation completion
func waitForOperation(ctx context.Context, t *testing.T, check func() (bool, error)) {
    require := require.New(t)

    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    timeout := time.After(10 * time.Second)

    for {
        select {
        case <-ticker.C:
            complete, err := check()
            require.NoError(err)
            if complete {
                return
            }
        case <-timeout:
            t.Fatal("operation timed out")
        case <-ctx.Done():
            t.Fatal("context cancelled")
        }
    }
}

// Helper to create test data
func createTestData(t *testing.T) []byte {
    return []byte("test-data")
}