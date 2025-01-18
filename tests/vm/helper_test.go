// tests/vm/helper_test.go
package vm_test

import (
    "context"
    "time"
)

// Test helpers and utilities
func waitForReady(ctx context.Context, vm *TestVM) error {
    deadline := time.Now().Add(10 * time.Second)
    for time.Now().Before(deadline) {
        if err := vm.ping(ctx); err == nil {
            return nil
        }
        time.Sleep(time.Second)
    }
    return context.DeadlineExceeded
}