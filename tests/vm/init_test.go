// tests/vm/init_test.go
package vm_test

import (
    "os"
    "testing"
)

func TestMain(m *testing.M) {
    // Setup code if needed
    code := m.Run()
    // Cleanup code if needed
    os.Exit(code)
}