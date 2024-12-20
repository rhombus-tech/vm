// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package storage

import "errors"

var (
    // Existing errors
    ErrInvalidAddress = errors.New("invalid address")
    ErrInvalidBalance = errors.New("invalid balance")

    // Object errors
    ErrObjectNotFound  = errors.New("object not found")
    ErrInvalidObjectID = errors.New("invalid object ID")
    
    // Event errors
    ErrInvalidEvent     = errors.New("invalid event")
    ErrEventQueueFull   = errors.New("event queue full")
    
    // Region errors
    ErrInvalidRegion    = errors.New("invalid region")
    ErrRegionNotFound   = errors.New("region not found")

    // State errors
    ErrInvalidState     = errors.New("invalid state")
    ErrStateNotFound    = errors.New("state not found")
)
