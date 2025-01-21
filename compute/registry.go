// compute/registry.go
package compute

type TEERegistry struct {
    config *Config
}

func NewTEERegistry(config *Config) *TEERegistry {
    return &TEERegistry{
        config: config,
    }
}