// core/types.go
package core

import (
    "errors"
    "time"
)

// TEE Types
type TEEAttestation struct {
    EnclaveID   []byte
    Measurement []byte
    Timestamp   time.Time
    Data        []byte
    Signature   []byte
    RegionProof []byte
}

// Object/Event Types
type ObjectState struct {
    Code        []byte
    Storage     []byte
    RegionID    string
    Events      []string
    LastUpdated time.Time
    Status      string
}

type Event struct {
    FunctionCall string
    Parameters   []byte
    Attestations [2]TEEAttestation
    Timestamp    string
    Status      string
}

// Coordination Types
type WorkerID string

type Task struct {
    ID           string
    WorkerIDs    []WorkerID
    Data         []byte
    Attestations [][]byte
    Timeout      time.Duration
}

type Message struct {
    FromWorker WorkerID
    ToWorker   WorkerID
    Type       MessageType
    Data       []byte
    Timestamp  time.Time
}

type MessageType uint8

const (
    MessageTypeSync MessageType = iota
    MessageTypeData
    MessageTypeAttestation
    MessageTypeComplete
)

type WorkerStatus uint8

const (
    WorkerStatusIdle WorkerStatus = iota
    WorkerStatusBusy
    WorkerStatusError
)

// Common errors
var (
    ErrWorkerNotFound    = errors.New("worker not found")
    ErrChannelNotFound   = errors.New("secure channel not found") 
    ErrTimeout          = errors.New("coordination timeout")
    ErrInvalidMessage   = errors.New("invalid message")
    ErrChannelClosed    = errors.New("channel closed")
)

// TEE Types
type TEEType uint8

const (
    TEETypeSGX TEEType = iota + 1
    TEETypeSEV
)

type TEEAddress []byte

// Channel info for secure communication
type ChannelInfo struct {
    PartnerID   WorkerID
    SessionKey  []byte
    Created     time.Time
}