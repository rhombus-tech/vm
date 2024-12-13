package actions

import (
    "errors"
    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
    "github.com/rhombus-tech/hypersdk/x/contracts/runtime/events"
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

type TEEExecResult struct {
    ContractAddr []byte
    Events      []events.Event
    StateUpdates map[string][]byte
}

type TEEExecAction struct {
    RegionID     string
    TxData       []byte
    UserSig      []byte
    EnclaveType  string    // "SGX" or "SEV"
    EnclaveID    []byte
    ExecResult   TEEExecResult
    TEESig       []byte
    TimeStamps   []RoughtimeStamp
}

func (t *TEEExecAction) Marshal(p *codec.Packer) {
    p.PackString(t.RegionID)
    p.PackBytes(t.TxData)
    p.PackBytes(t.UserSig)
    p.PackString(t.EnclaveType)
    p.PackBytes(t.EnclaveID)
    
    // Pack ExecResult
    p.PackBytes(t.ExecResult.ContractAddr)
    
    // Pack Events
    p.PackInt(len(t.ExecResult.Events))
    for _, event := range t.ExecResult.Events {
        eventBytes, _ := event.Marshal()
        p.PackBytes(eventBytes)
    }
    
    // Pack StateUpdates
    p.PackInt(len(t.ExecResult.StateUpdates))
    for key, value := range t.ExecResult.StateUpdates {
        p.PackString(key)
        p.PackBytes(value)
    }
    
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

    // Unpack ExecResult
    contractAddr, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.ExecResult.ContractAddr = contractAddr

    // Unpack Events
    eventCount, err := p.UnpackInt()
    if err != nil {
        return nil, err
    }
    act.ExecResult.Events = make([]events.Event, eventCount)
    for i := 0; i < eventCount; i++ {
        eventBytes, err := p.UnpackBytes()
        if err != nil {
            return nil, err
        }
        event := events.Event{}
        if err := event.Unmarshal(eventBytes); err != nil {
            return nil, err
        }
        act.ExecResult.Events[i] = event
    }

    // Unpack StateUpdates
    updateCount, err := p.UnpackInt()
    if err != nil {
        return nil, err
    }
    act.ExecResult.StateUpdates = make(map[string][]byte, updateCount)
    for i := 0; i < updateCount; i++ {
        key, err := p.UnpackString()
        if err != nil {
            return nil, err
        }
        value, err := p.UnpackBytes()
        if err != nil {
            return nil, err
        }
        act.ExecResult.StateUpdates[key] = value
    }

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

    // 6. Process state updates
    for key, value := range t.ExecResult.StateUpdates {
        stateKey := state.Key("state", t.RegionID, []byte(key))
        if err := sm.Set(stateKey, value); err != nil {
            return err
        }
    }

    // 7. Store events
    for i, event := range t.ExecResult.Events {
        eventKey := state.Key("event", t.RegionID, t.ExecResult.ContractAddr, uint64(i))
        eventBytes, err := event.Marshal()
        if err != nil {
            return err
        }
        if err := sm.Set(eventKey, eventBytes); err != nil {
            return err
        }
    }

    return nil
}

func (t *TEEExecAction) StateKeys(chain.Auth) []string {
    keys := []string{
        state.Key("region", t.RegionID),
        state.Key("enclave", t.RegionID, t.EnclaveID),
        state.Key("enclave-pubkey", t.RegionID, t.EnclaveID),
    }

    // Add state update keys
    for key := range t.ExecResult.StateUpdates {
        keys = append(keys, state.Key("state", t.RegionID, []byte(key)))
    }

    // Add event keys
    for i := range t.ExecResult.Events {
        keys = append(keys, state.Key("event", t.RegionID, t.ExecResult.ContractAddr, uint64(i)))
    }

    return keys
}

func (t *TEEExecAction) MaxUnits(chain.Auth) uint64 {
    return chain.DefaultUnits
}

// Helper functions

func verifyTEESignature(result TEEExecResult, sig, pubKey []byte, enclaveType string) bool {
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
