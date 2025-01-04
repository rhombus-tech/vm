package coordination

import "time"

type Config struct {
    // Worker settings
    MinWorkers          int
    MaxWorkers          int
    WorkerTimeout       time.Duration
    
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
}

func DefaultConfig() *Config {
    return &Config{
        MinWorkers:         2,
        MaxWorkers:         10,
        WorkerTimeout:      30 * time.Second,
        ChannelTimeout:     10 * time.Second,
        MaxMessageSize:     1024 * 1024, // 1MB
        EncryptionEnabled:  true,
        RequireAttestation: true,
        AttestationTimeout: 5 * time.Second,
        StoragePath:        "/tmp/coordinator",
        PersistenceEnabled: true,
    }
}