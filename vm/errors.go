// vm/errors.go
package vm

import "errors"

var (
    ErrInvalidRegionID = errors.New("invalid region ID")
    ErrInvalidTEE     = errors.New("invalid TEE configuration")
    ErrInvalidRegion  = errors.New("invalid region configuration")
    ErrRegionHealthUnavailable = errors.New("region health monitoring unavailable")
    ErrRegionMetricsUnavailable = errors.New("region metrics unavailable")
)
