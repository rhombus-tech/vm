// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package consts

import (
   "github.com/ava-labs/avalanchego/ids"
   "github.com/ava-labs/avalanchego/version"
)

const (
   HRP      = "morpheus"
   Name     = "morpheusvm"
   Symbol   = "RED"
   Decimals = 9

   // Size limits for ShuttleVM
   MaxCodeSize    = 1024 * 1024    // 1MB
   MaxStorageSize = 1024 * 1024    // 1MB
   MaxIDLength    = 256

   // TEE constants
   TEETypeSGX uint8 = 1
   TEETypeSEV uint8 = 2

   // Time window constants
   MaxTimeDrift = 5 * 60  // 5 minutes in seconds
   MinTimeDrift = -5 * 60 // 5 minutes in seconds

   // Attestation limits
   MaxAttestationSize = 1024  // Maximum size of TEE attestation in bytes
)

var ID ids.ID

func init() {
   b := make([]byte, ids.IDLen)
   copy(b, []byte(Name))
   vmID, err := ids.ToID(b)
   if err != nil {
       panic(err)
   }
   ID = vmID
}

var Version = &version.Semantic{
   Major: 0,
   Minor: 0,
   Patch: 1,
}
