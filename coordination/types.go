package coordination

import (
    "errors"
    "time"
)


const (
    MessageTypeTask MessageType = iota
    MessageTypeStatus
    MessageTypeResult
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
    RegionID     string
}

// WorkerState tracks worker status
type WorkerState struct {
    ID          WorkerID
    EnclaveID   []byte
    Status      WorkerStatus
    Partners    map[WorkerID]*ChannelInfo
    LastActive  time.Time
}

type ChannelInfo struct {
    PartnerID  WorkerID
    SessionKey []byte
    Created    time.Time
}

type TEEPair struct {
    SGXID []byte
    SEVID []byte
}