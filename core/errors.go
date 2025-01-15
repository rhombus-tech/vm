// core/errors.go
package core

import "errors"

var (
    ErrRegionNotFound      = errors.New("region not found")
    ErrInvalidRegionalKey  = errors.New("invalid regional key format")
    ErrRegionAccessDenied  = errors.New("region access denied")
    ErrInvalidProof        = errors.New("invalid merkle proof")
)
