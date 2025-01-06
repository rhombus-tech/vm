// core/coordination_types.go
package core

import ( 
    "time"
)

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

type ChannelInfo struct {
    PartnerID   WorkerID
    SessionKey  []byte
    Created     time.Time
}