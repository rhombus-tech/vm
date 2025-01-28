// coordination/errors.go
package coordination

import "errors"

var (
    ErrNotEnoughWorkers  = errors.New("not enough workers available")
    ErrInvalidWorker     = errors.New("invalid worker")
    ErrTaskRejected      = errors.New("task rejected")
    ErrCoordinationError = errors.New("coordination error")
    ErrInvalidState      = errors.New("invalid coordination state")
    ErrStorageError      = errors.New("storage error")
    ErrBatchError        = errors.New("batch operation error")
    ErrRegionNotFound = errors.New("region not found")
    ErrInvalidRegionID = errors.New("invalid region ID")
    ErrRegionExists    = errors.New("region already exists")
)