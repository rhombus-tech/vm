package coordination

import "time"

type Config struct {
    // Worker settings
    MinWorkers          int
    MaxWorkers          int
    WorkerTimeout       time.Duration
    
    // Task settings (new section)
    MaxTasks            int           // Maximum number of concurrent tasks
    TaskQueueSize       int           // Size of task queue buffer
    TaskTimeout         time.Duration // Maximum time for task execution
    TaskCleanupInterval time.Duration // How often to clean up completed tasks
    
    // Channel settings
    ChannelTimeout      time.Duration
    MaxMessageSize      int
    EncryptionEnabled   bool

    // TEE settings
    RequireAttestation  bool
    AttestationTimeout  time.Duration
    
    // Storage settings
    StoragePath         string
    PersistenceEnabled  bool

     MaxObjects          int           `json:"max_objects"`
    MaxEvents          int           `json:"max_events"`
}


func DefaultConfig() *Config {
    return &Config{
        // Existing worker settings
        MinWorkers:          2,
        MaxWorkers:          10,
        WorkerTimeout:       30 * time.Second,
        
        // New task settings
        MaxTasks:            100,
        TaskQueueSize:       1000,
        TaskTimeout:         5 * time.Minute,
        TaskCleanupInterval: time.Hour,
        
        // Existing channel settings
        ChannelTimeout:      10 * time.Second,
        MaxMessageSize:      1024 * 1024, // 1MB
        EncryptionEnabled:   true,

        // Existing TEE settings
        RequireAttestation:  true,
        AttestationTimeout:  5 * time.Second,
        
        // Existing storage settings
        StoragePath:         "",
        PersistenceEnabled:  true,
    }
}