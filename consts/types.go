// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package consts

const (
    // Action TypeIDs
    TransferID                   uint8 = 0
    ContractVerificationID       uint8 = 1
    ContractVerificationResultID uint8 = 2
    // ShuttleVM Action TypeIDs
    CreateObjectID         uint8 = 3
    SetInputObjectID       uint8 = 4
    SendEventID           uint8 = 5
    CreateRegionID        uint8 = 6
    UpdateRegionID        uint8 = 7
    // ShuttleVM Result TypeIDs
    CreateObjectResultID   uint8 = 8
    SetInputObjectResultID uint8 = 9
    SendEventResultID      uint8 = 10
    CreateRegionResultID   uint8 = 11
    UpdateRegionResultID   uint8 = 12
)

// ShuttleVM Error Types
var (
    ErrObjectExists     = "object already exists"
    ErrObjectNotFound   = "object not found"
    ErrInvalidID        = "invalid object ID"
    ErrCodeTooLarge     = "code size exceeds maximum"
    ErrStorageTooLarge  = "storage size exceeds maximum"
    ErrInvalidFunction  = "invalid function call"
    ErrRegionExists     = "region already exists"
    ErrRegionNotFound   = "region not found"
    ErrInvalidTEE       = "invalid TEE address"
)
