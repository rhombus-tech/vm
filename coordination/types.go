package coordination

import (
    "errors"
    "time"
)

type WorkerID string

var (
    ErrWorkerNotFound    = errors.New("worker not found")
    ErrChannelNotFound   = errors.New("secure channel not found")
    ErrTimeout          = errors.New("coordination timeout")
    ErrInvalidMessage   = errors.New("invalid message")
    ErrChannelClosed    = errors.New("channel closed")
)

// Task represents a unit of coordinated work
type Task struct {
    ID           string
    WorkerIDs    []WorkerID  // Workers that need to coordinate
    Data         []byte      // Task data
    Attestations [][]byte    // TEE attestations
    Timeout      time.Duration
}

// Message represents communication between workers
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

// WorkerState tracks worker status
type WorkerState struct {
    ID          WorkerID
    EnclaveID   []byte
    Status      WorkerStatus
    Partners    map[WorkerID]*ChannelInfo
    LastActive  time.Time
}

type WorkerStatus uint8

const (
    WorkerStatusIdle WorkerStatus = iota
    WorkerStatusBusy
    WorkerStatusError
)

type ChannelInfo struct {
    PartnerID  WorkerID
    SessionKey []byte
    Created    time.Time
}