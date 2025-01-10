// tee/config.go
package tee

type TEEConfig struct { 
    SGXEndpoint string
    SEVEndpoint string
    RequireDualAttestation bool
}

func DefaultConfig() TEEConfig {
    return TEEConfig{
        SGXEndpoint: "localhost:50051",
        SEVEndpoint: "localhost:50052",
        RequireDualAttestation: true,
    }
}