# ShuttleVM

ShuttleVM is a general-purpose virtual machine built on HyperSDK that implements an object-event model for regional execution in Trusted Execution Environments (TEEs).

## Architecture

ShuttleVM consists of:

1. Object System
- Objects contain code and storage
- Code executes in TEEs
- Objects communicate through events

2. Event System
- Events trigger object functions
- Regional execution via ping-pong protocol

3. Regional Execution
- TEE-based execution
- Regional state management
- Cross-region coordination

## Core Actions

ShuttleVM supports three fundamental actions:

1. `CreateObjectAction`
- Create new objects with code and storage
- Basic validation of object properties
- Object lifecycle management

2. `SendEventAction`
- Send events between objects
- Queue management with priorities
- Event parameter handling

3. `SetInputObjectAction`
- Designate objects for receiving extrinsics
- System configuration management

## Getting Started

### Prerequisites
- Docker
- Go v1.22.5+
- NodeJS v20+

### Launch Steps
1. Clone the repository
2. Build and start the network
3. Deploy objects
4. Send events

### Example Usage
```go
// Create an object
createAction := &actions.CreateObjectAction{
    ID:      "myObject",
    Code:    myCode,
    Storage: initialStorage,
}

// Send an event
eventAction := &actions.SendEventAction{
    IDTo:         "myObject",
    FunctionCall: "myFunction",
    Parameters:   params,
}
