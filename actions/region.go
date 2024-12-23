// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package actions

import (
   "context"
   "errors"

   "github.com/ava-labs/hypersdk/chain"
   "github.com/ava-labs/hypersdk/codec"
)

var (
   ErrRegionExists    = errors.New("region already exists")
   ErrRegionNotFound  = errors.New("region not found")
   ErrInvalidTEE      = errors.New("invalid TEE")
   ErrInvalidRegionID = errors.New("invalid region ID")
)

type TEEAddress []byte

type CreateRegionAction struct {
   RegionID string       `json:"region_id"`
   TEEs     []TEEAddress `json:"tees"`
   Attestations [2]TEEAttestation // Attestations from admin TEE pair
}

func (*CreateRegionAction) GetTypeID() uint8 { return CreateRegion }

func (a *CreateRegionAction) Marshal(p *codec.Packer) {
   p.PackString(a.RegionID)
   p.PackInt(len(a.TEEs))
   for _, tee := range a.TEEs {
       p.PackBytes(tee)
   }
   a.Attestations[0].Marshal(p)
   a.Attestations[1].Marshal(p)
}

func UnmarshalCreateRegion(p *codec.Packer) (chain.Action, error) {
   var act CreateRegionAction
   regionID, err := p.UnpackString()
   if err != nil {
       return nil, err
   }
   act.RegionID = regionID

   numTEEs, err := p.UnpackInt()
   if err != nil {
       return nil, err
   }

   act.TEEs = make([]TEEAddress, numTEEs)
   for i := 0; i < numTEEs; i++ {
       tee, err := p.UnpackBytes()
       if err != nil {
           return nil, err
       }
       act.TEEs[i] = tee
   }

   att0, err := UnmarshalAttestation(p)
   if err != nil {
       return nil, err
   }
   act.Attestations[0] = att0

   att1, err := UnmarshalAttestation(p)
   if err != nil {
       return nil, err
   }
   act.Attestations[1] = att1

   return &act, nil
}

func (a *CreateRegionAction) Verify(ctx context.Context, vm chain.VM) error {
    if len(a.RegionID) == 0 || len(a.RegionID) > 256 {
        return ErrInvalidRegionID
    }
    if len(a.TEEs) == 0 {
        return ErrInvalidTEE
    }
    for _, tee := range a.TEEs {
        if len(tee) == 0 {
            return ErrInvalidTEE
        }
    }
    return verifyAttestationPair(a.Attestations)
}

func (a *UpdateRegionAction) Verify(ctx context.Context, vm chain.VM) error {
    if len(a.RegionID) == 0 || len(a.RegionID) > 256 {
        return ErrInvalidRegionID
    }
    if len(a.AddTEEs) == 0 && len(a.RemTEEs) == 0 {
        return ErrInvalidTEE
    }
    for _, tee := range a.AddTEEs {
        if len(tee) == 0 {
            return ErrInvalidTEE
        }
    }
    for _, tee := range a.RemTEEs {
        if len(tee) == 0 {
            return ErrInvalidTEE
        }
    }
    return verifyAttestationPair(a.Attestations)
}

func (a *CreateRegionAction) Execute(ctx context.Context, vm chain.VM) (*CreateRegionResult, error) {
   key := []byte("region:" + a.RegionID)
   
   exists, err := vm.State().Has(ctx, key)
   if err != nil {
       return nil, err
   }
   if exists {
       return &CreateRegionResult{
           RegionID: a.RegionID,
           Success: false,
       }, ErrRegionExists
   }
   
   region := map[string]interface{}{
       "tees": a.TEEs,
       "attestations": a.Attestations,
   }
   
   regionBytes, err := codec.Marshal(region)
   if err != nil {
       return nil, err
   }
   
   if err := vm.State().Set(ctx, key, regionBytes); err != nil {
       return nil, err
   }
   
   return &CreateRegionResult{
       RegionID: a.RegionID,
       Success: true,
       StateHash: a.Attestations[0].Data,
       Timestamp: a.Attestations[0].Timestamp,
   }, nil
}

type UpdateRegionAction struct {
   RegionID string       `json:"region_id"`
   AddTEEs  []TEEAddress `json:"add_tees"`
   RemTEEs  []TEEAddress `json:"rem_tees"`
   Attestations [2]TEEAttestation // Attestations from admin TEE pair
}

func (*UpdateRegionAction) GetTypeID() uint8 { return UpdateRegion }

func (a *UpdateRegionAction) Marshal(p *codec.Packer) {
   p.PackString(a.RegionID)
   p.PackInt(len(a.AddTEEs))
   for _, tee := range a.AddTEEs {
       p.PackBytes(tee)
   }
   p.PackInt(len(a.RemTEEs))
   for _, tee := range a.RemTEEs {
       p.PackBytes(tee)
   }
   a.Attestations[0].Marshal(p)
   a.Attestations[1].Marshal(p)
}

func UnmarshalUpdateRegion(p *codec.Packer) (chain.Action, error) {
   var act UpdateRegionAction
   regionID, err := p.UnpackString()
   if err != nil {
       return nil, err
   }
   act.RegionID = regionID

   numAddTEEs, err := p.UnpackInt()
   if err != nil {
       return nil, err
   }
   act.AddTEEs = make([]TEEAddress, numAddTEEs)
   for i := 0; i < numAddTEEs; i++ {
       tee, err := p.UnpackBytes()
       if err != nil {
           return nil, err
       }
       act.AddTEEs[i] = tee
   }

   numRemTEEs, err := p.UnpackInt()
   if err != nil {
       return nil, err
   }
   act.RemTEEs = make([]TEEAddress, numRemTEEs)
   for i := 0; i < numRemTEEs; i++ {
       tee, err := p.UnpackBytes()
       if err != nil {
           return nil, err
       }
       act.RemTEEs[i] = tee
   }

   att0, err := UnmarshalAttestation(p)
   if err != nil {
       return nil, err
   }
   act.Attestations[0] = att0

   att1, err := UnmarshalAttestation(p)
   if err != nil {
       return nil, err
   }
   act.Attestations[1] = att1

   return &act, nil
}

func (a *UpdateRegionAction) Execute(ctx context.Context, vm chain.VM) (*UpdateRegionResult, error) {
   key := []byte("region:" + a.RegionID)
   
   regionBytes, err := vm.State().Get(ctx, key)
   if err != nil {
       return nil, err
   }
   if regionBytes == nil {
       return &UpdateRegionResult{
           RegionID: a.RegionID,
           Success: false,
       }, ErrRegionNotFound
   }
   
   var region map[string]interface{}
   if err := codec.Unmarshal(regionBytes, &region); err != nil {
       return nil, err
   }
   
   currentTEEs := region["tees"].([]TEEAddress)
   
   // Remove TEEs
   for _, remTEE := range a.RemTEEs {
       for i, tee := range currentTEEs {
           if bytes.Equal(tee, remTEE) {
               currentTEEs = append(currentTEEs[:i], currentTEEs[i+1:]...)
               break
           }
       }
   }
   
   // Add new TEEs
   currentTEEs = append(currentTEEs, a.AddTEEs...)
   
   region["tees"] = currentTEEs
   region["attestations"] = a.Attestations
   
   newRegionBytes, err := codec.Marshal(region)
   if err != nil {
       return nil, err
   }
   
   if err := vm.State().Set(ctx, key, newRegionBytes); err != nil {
       return nil, err
   }
   
   return &UpdateRegionResult{
       RegionID: a.RegionID,
       Success: true,
       StateHash: a.Attestations[0].Data,
       Timestamp: a.Attestations[0].Timestamp,
   }, nil
}

type CreateRegionResult struct {
   RegionID   string `json:"region_id"`
   Success    bool   `json:"success"`
   StateHash  []byte `json:"state_hash"`
   Timestamp  string `json:"timestamp"`
}

func (*CreateRegionResult) GetTypeID() uint8 { return CreateRegion }

type UpdateRegionResult struct {
   RegionID   string `json:"region_id"`
   Success    bool   `json:"success"`
   StateHash  []byte `json:"state_hash"`
   Timestamp  string `json:"timestamp"`
}

func (*UpdateRegionResult) GetTypeID() uint8 { return UpdateRegion }

func (r *CreateRegionResult) Marshal(p *codec.Packer) {
    p.PackString(r.RegionID)
    p.PackBool(r.Success)
    p.PackBytes(r.StateHash)
    p.PackString(r.Timestamp)
}

func UnmarshalCreateRegionResult(p *codec.Packer) (codec.Typed, error) {
    var res CreateRegionResult
    regionID, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    res.RegionID = regionID

    success, err := p.UnpackBool()
    if err != nil {
        return nil, err
    }
    res.Success = success

    stateHash, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    res.StateHash = stateHash

    timestamp, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    res.Timestamp = timestamp
    
    return &res, nil
}

func (r *UpdateRegionResult) Marshal(p *codec.Packer) {
    p.PackString(r.RegionID)
    p.PackBool(r.Success)
    p.PackBytes(r.StateHash)
    p.PackString(r.Timestamp)
}

func UnmarshalUpdateRegionResult(p *codec.Packer) (codec.Typed, error) {
    var res UpdateRegionResult
    regionID, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    res.RegionID = regionID

    success, err := p.UnpackBool()
    if err != nil {
        return nil, err
    }
    res.Success = success

    stateHash, err := p.UnpackBytes()
    if err != nil {
        return nil, err
    }
    res.StateHash = stateHash

    timestamp, err := p.UnpackString()
    if err != nil {
        return nil, err
    }
    res.Timestamp = timestamp
    
    return &res, nil
}

// Update RegisterActions
func RegisterActions(f *chain.AuthFactory) {
   f.Register(&CreateObjectAction{}, UnmarshalCreateObject)
   f.Register(&SendEventAction{}, UnmarshalSendEvent)
   f.Register(&SetInputObjectAction{}, UnmarshalSetInputObject)
   f.Register(&CreateRegionAction{}, UnmarshalCreateRegion)
   f.Register(&UpdateRegionAction{}, UnmarshalUpdateRegion)
}
