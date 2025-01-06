// tee/config.go
package tee

type Config struct {
    SGXEndpoint string
    SEVEndpoint string
    RequireDualAttestation bool
}

func DefaultConfig() Config {
    return Config{
        SGXEndpoint: "localhost:50051",
        SEVEndpoint: "localhost:50052",
        RequireDualAttestation: true,
    }
}