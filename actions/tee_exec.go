package actions

import (
    "errors"
    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
    "sort"
)

var (
    ErrInvalidRegion = errors.New("invalid region")
    ErrInvalidEnclave = errors.New("invalid enclave")
    ErrInvalidSignature = errors.New("invalid signature")
    ErrInvalidTimeStamps = errors.New("invalid timestamps")
    ErrStaleTimeStamp = errors.New("stale timestamp")
    ErrInvalidExecResult = errors.New("invalid execution result")
)

type RoughtimeStamp struct {
    ServerID  string
    Time      uint64
    Signature []byte
}

type TEEExecAction struct {
    RegionID     string
    TxData       []byte
    UserSig      []byte
    EnclaveType  string    // "SGX" or "SEV"
    EnclaveID    []byte
    ExecResult   []byte
    TEESig       []byte
    TimeStamps   []RoughtimeStamp
}

func (t *TEEExecAction) Marshal(p *codec.Packer) {
    p.PackString(t.RegionID)
    p.PackBytes(t.TxData)
    p.PackBytes(t.UserSig)
    p.PackString(t.EnclaveType)
    p.PackBytes(t.EnclaveID)
    p.PackBytes(t.ExecResult)
    p.PackBytes(t.TEESig)
    
    // Pack TimeStamps
    p.PackInt(len(t.TimeStamps))
    for _, ts := range t.TimeStamps {
        p.PackString(ts.ServerID)
        p.PackUint64(ts.Time)
        p.PackBytes(ts.Signature)
    }
}

func UnmarshalTEEExecAction(p *codec.Packer) (*TEEExecAction, error) {
    var act TEEExecAction

    regionID, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.RegionID = regionID

    txData, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.TxData = txData

    userSig, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.UserSig = userSig

    enclaveType, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.EnclaveType = enclaveType

    enclaveID, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.EnclaveID = enclaveID

    execResult, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.ExecResult = execResult

    teeSig, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.TEESig = teeSig

    // Unpack TimeStamps
    tsLen, err := p.UnpackInt()
    if err != nil {
        return nil, err
    }

    act.TimeStamps = make([]RoughtimeStamp, tsLen)
    for i := 0; i < tsLen; i++ {
        serverID, err := p.UnpackString()
        if err != nil {
            return nil, err
        }

        time, err := p.UnpackUint64()
        if err != nil {
            return nil, err
        }

        sig, err := p.UnpackBytes()
        if err != nil {
            return nil, err
        }

        act.TimeStamps[i] = RoughtimeStamp{
            ServerID:  serverID,
            Time:      time,
            Signature: sig,
        }
    }

    return &act, nil
}

func (t *TEEExecAction) Execute(ctx chain.Context) error {
    sm := state.NewManager(ctx)

    // 1. Verify Region
    regionKey := state.Key("region", t.RegionID)
    exists, err := sm.Exists(regionKey)
    if err != nil {
        return err
    }
    if !exists {
        return ErrInvalidRegion
    }

    // 2. Verify Enclave is registered and active
    enclaveKey := state.Key("enclave", t.RegionID, t.EnclaveID)
    enclaveStatus, err := sm.Get(enclaveKey)
    if err != nil {
        return err
    }
    if enclaveStatus == nil || enclaveStatus[0] != 1 { // 1 = active
        return ErrInvalidEnclave
    }

    // 3. Get Enclave Public Key and verify TEE signature
    pubKeyKey := state.Key("enclave-pubkey", t.RegionID, t.EnclaveID)
    pubKey, err := sm.Get(pubKeyKey)
    if err != nil {
        return err
    }
    if !verifyTEESignature(t.ExecResult, t.TEESig, pubKey, t.EnclaveType) {
        return ErrInvalidSignature
    }

    // 4. Verify Roughtime stamps
    medianTime, err := verifyTimeStamps(t.TimeStamps)
    if err != nil {
        return err
    }

    // 5. Check if timestamp is within acceptable range
    if !isTimeStampValid(medianTime, ctx.Time()) {
        return ErrStaleTimeStamp
    }

    // 6. Process execution result
    if err := processExecResult(sm, t.RegionID, t.ExecResult); err != nil {
        return err
    }

    // 7. Store transaction record
    txKey := state.Key("tx", t.RegionID, hash(t.TxData))
    return sm.Set(txKey, t.ExecResult)
}

func (t *TEEExecAction) StateKeys(chain.Auth) []string {
    return []string{
        state.Key("region", t.RegionID),
        state.Key("enclave", t.RegionID, t.EnclaveID),
        state.Key("enclave-pubkey", t.RegionID, t.EnclaveID),
        state.Key("tx", t.RegionID, hash(t.TxData)),
    }
}

func (t *TEEExecAction) MaxUnits(chain.Auth) uint64 {
    return chain.DefaultUnits
}

// Helper functions

func verifyTEESignature(result, sig, pubKey []byte, enclaveType string) bool {
    // Implement signature verification based on enclave type
    return true // placeholder
}

func verifyTimeStamps(stamps []RoughtimeStamp) (uint64, error) {
    if len(stamps) < 3 {
        return 0, ErrInvalidTimeStamps
    }

    times := make([]uint64, len(stamps))
    for i, stamp := range stamps {
        if !verifyRoughtimeStamp(stamp) {
            return 0, ErrInvalidTimeStamps
        }
        times[i] = stamp.Time
    }

    sort.Slice(times, func(i, j int) bool {
        return times[i] < times[j]
    })

    return times[len(times)/2], nil
}

func verifyRoughtimeStamp(stamp RoughtimeStamp) bool {
    // Implement Roughtime signature verification
    return true // placeholder
}

func isTimeStampValid(stampTime, currentTime uint64) bool {
    // Check if timestamp is within acceptable range (e.g., 5 minutes)
    const maxDrift = 5 * 60 // 5 minutes in seconds
    diff := currentTime - stampTime
    if diff < 0 {
        diff = -diff
    }
    return diff <= maxDrift
}

func processExecResult(sm *state.Manager, regionID string, result []byte) error {
    // Process and store state changes from execution result
    // This would depend on your specific execution result format
    return nil // placeholder
}

func hash(data []byte) []byte {
    // Implement appropriate hashing function
    return data // placeholder
}
