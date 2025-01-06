// core/config.go
package core

type Config struct {
    EnclaveType    PlatformType
    TeeEndpoint    string
    AttestationKey []byte
    Debug          bool
}

func DefaultConfig() *Config {
    return &Config{
        EnclaveType: PlatformTypeSGX,
        Debug:       false,
    }
}