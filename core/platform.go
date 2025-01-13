// core/platform.go
package core

import (
	"context"
	"errors"
)

type PlatformType uint8

const (
    PlatformTypeSGX PlatformType = iota + 1
    PlatformTypeSEV
)

type Platform interface {
    Type() PlatformType
    Execute(ctx context.Context, code []byte, input []byte) (*ExecutionResult, error)
    Verify(attestation *TEEAttestation) error
    Close() error
}

func NewPlatform(pType PlatformType, config *Config) (Platform, error) {
    switch pType {
    case PlatformTypeSGX:
        return NewSGXPlatform(config)
    case PlatformTypeSEV:
        return NewSEVPlatform(config)
    default:
        return nil, errors.New("unsupported platform type")
    }
}
