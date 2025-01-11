package vm

import "errors"

var (
    ErrInvalidRegionID = errors.New("invalid region ID")
    ErrInvalidTEE     = errors.New("invalid TEE configuration")
    ErrInvalidRegion  = errors.New("invalid region configuration")
    
    // Add any other common errors here
    ErrRegionNotFound  = errors.New("region not found")
    ErrRegionExists    = errors.New("region already exists")
    ErrTEEMismatch     = errors.New("TEE mismatch")
    ErrInvalidStatus   = errors.New("invalid region status")
)