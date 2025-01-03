package vm

import (
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/coordination"
)

// StateManager extends the chain.StateManager interface to add coordination capabilities
type StateManager interface {
    chain.StateManager
    
    // GetCoordinator returns the coordinator instance for managing regional execution
    GetCoordinator() *coordination.Coordinator
}

// VM extends the chain.VM interface to add coordination capabilities 
type VM interface {
    chain.VM
    
    // Coordinator returns the VM's coordinator instance
    Coordinator() *coordination.Coordinator
}