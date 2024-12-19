package actions

import (
    "context"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/consts"
)

const (
    CreateObject uint8 = iota
    DeleteObject
    ChangeObjectCode
    ChangeObjectStorage
    SetInputObject
    SendEvent
)

type CreateObjectAction struct {
    ID      string `json:"id"`
    Code    []byte `json:"code"`
    Storage []byte `json:"storage"`
}

func (*CreateObjectAction) GetTypeID() uint8 { return CreateObject }

func (a *CreateObjectAction) Marshal(p *codec.Packer) {
    p.PackString(a.ID)
    p.PackBytes(a.Code)
    p.PackBytes(a.Storage)
}

func UnmarshalCreateObject(p *codec.Packer) (chain.Action, error) {
    var act CreateObjectAction
    id, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.ID = id

    code, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.Code = code

    storage, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.Storage = storage

    return &act, nil
}

type SendEventAction struct {
    Priority      uint64 `json:"priority"`
    IDTo          string `json:"id_to"`
    FunctionCall  string `json:"function_call"`
    Parameters    []byte `json:"parameters"`
}

func (*SendEventAction) GetTypeID() uint8 { return SendEvent }

func (a *SendEventAction) Marshal(p *codec.Packer) {
    p.PackUint64(a.Priority)
    p.PackString(a.IDTo)
    p.PackString(a.FunctionCall)
    p.PackBytes(a.Parameters)
}

func UnmarshalSendEvent(p *codec.Packer) (chain.Action, error) {
    var act SendEventAction
    
    priority, err := p.UnpackUint64()
    if err != nil {
        return nil, err
    }
    act.Priority = priority
    
    idTo, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.IDTo = idTo
    
    functionCall, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.FunctionCall = functionCall
    
    parameters, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.Parameters = parameters
    
    return &act, nil
}

type ChangeObjectCodeAction struct {
    ID      string `json:"id"`
    NewCode []byte `json:"new_code"`
}

func (*ChangeObjectCodeAction) GetTypeID() uint8 { return ChangeObjectCode }

func (a *ChangeObjectCodeAction) Marshal(p *codec.Packer) {
    p.PackString(a.ID)
    p.PackBytes(a.NewCode)
}

func UnmarshalChangeObjectCode(p *codec.Packer) (chain.Action, error) {
    var act ChangeObjectCodeAction
    
    id, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    act.ID = id
    
    newCode, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    act.NewCode = newCode
    
    return &act, nil
}

// RegisterActions registers all ShuttleVM actions with the auth factory
func RegisterActions(f *chain.AuthFactory) {
    f.Register(&CreateObjectAction{}, UnmarshalCreateObject)
    f.Register(&SendEventAction{}, UnmarshalSendEvent)
    f.Register(&ChangeObjectCodeAction{}, UnmarshalChangeObjectCode)
    // Register other actions...
}
