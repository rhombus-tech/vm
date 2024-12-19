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
    DeleteObjectID         uint8 = 4
    ChangeObjectCodeID     uint8 = 5
    ChangeObjectStorageID  uint8 = 6
    SetInputObjectID       uint8 = 7
    SendEventID           uint8 = 8

    // ShuttleVM Result TypeIDs
    CreateObjectResultID        uint8 = 9
    DeleteObjectResultID        uint8 = 10
    ChangeObjectCodeResultID    uint8 = 11
    ChangeObjectStorageResultID uint8 = 12
    SetInputObjectResultID      uint8 = 13
    SendEventResultID          uint8 = 14
)

// ShuttleVM Error Types
var (
    ErrObjectExists     = "object already exists"
    ErrObjectNotFound   = "object not found"
    ErrInvalidID        = "invalid object ID"
    ErrUnauthorized     = "unauthorized operation"
    ErrCodeTooLarge     = "code size exceeds maximum"
    ErrStorageTooLarge  = "storage size exceeds maximum"
    ErrInvalidFunction  = "invalid function call"
)
