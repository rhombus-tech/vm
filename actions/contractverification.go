package actions

import (
    "bytes"
    "context"
    "crypto/sha256"
    "errors"
    "fmt"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/crypto/ed25519"
    "github.com/ava-labs/hypersdk/state"
    "github.com/ava-labs/hypersdk/examples/shuttlevm/storage"
    "github.com/ava-labs/hypersdk/examples/shuttlevm/consts"
)

var (
    ErrInvalidSignature     = errors.New("invalid signature")
    ErrChecksumMismatch     = errors.New("checksum mismatch")
    ErrContractExecution    = errors.New("contract execution failed")
    ErrInvalidContractCode  = errors.New("invalid contract code")
    
    _ chain.Action = (*ContractVerification)(nil)
)

const (
    MinContractSize = 1
    MaxContractSize = 1024 * 1024 // 1MB max contract size
    BaseComputeUnits = 1000
)

type ContractVerification struct {
    // Contract code to be verified
    ContractCode []byte `serialize:"true" json:"contract_code"`
    
    // Signature of the contract code
    Signature []byte `serialize:"true" json:"signature"`
    
    // Public key used to verify the signature
    PublicKey []byte `serialize:"true" json:"public_key"`
    
    // Expected checksum of the contract execution results
    ExpectedChecksum []byte `serialize:"true" json:"expected_checksum"`
}

func (*ContractVerification) GetTypeID() uint8 {
    return consts.ContractVerificationID
}

func (cv *ContractVerification) StateKeys(actor codec.Address, _ ids.ID) state.Keys {
    return state.Keys{
        string(storage.ContractKey(cv.ExpectedChecksum)): state.Read | state.Write,
        string(storage.BalanceKey(actor)):                state.Read | state.Write,
    }
}

func (cv *ContractVerification) Execute(
    ctx context.Context,
    rules chain.Rules,
    mu state.Mutable,
    timestamp int64,
    actor codec.Address,
    txID ids.ID,
) (codec.Typed, error) {
    // Basic validation
    if len(cv.ContractCode) < MinContractSize || len(cv.ContractCode) > MaxContractSize {
        return nil, ErrInvalidContractCode
    }

    // Verify signature
    if err := verifySignature(cv.ContractCode, cv.Signature, cv.PublicKey); err != nil {
        return nil, fmt.Errorf("%w: %s", ErrInvalidSignature, err)
    }

    // Calculate contract checksum
    actualChecksum := calculateChecksum(cv.ContractCode)
    
    // Compare with expected checksum
    if !bytes.Equal(actualChecksum, cv.ExpectedChecksum) {
        return nil, ErrChecksumMismatch
    }

    // Execute contract and verify results
    results, err := executeContract(cv.ContractCode)
    if err != nil {
        return nil, fmt.Errorf("%w: %s", ErrContractExecution, err)
    }

    // Store contract if verification successful
    if err := storage.StoreContract(ctx, mu, cv.ContractCode, actualChecksum); err != nil {
        return nil, fmt.Errorf("failed to store contract: %w", err)
    }

    return &ContractVerificationResult{
        Success:          true,
        ExecutionResults: results,
        Checksum:        actualChecksum,
    }, nil
}

func (cv *ContractVerification) ComputeUnits(chain.Rules) uint64 {
    // Base cost plus additional cost based on contract size
    return BaseComputeUnits + uint64(len(cv.ContractCode)/1024)
}

func (*ContractVerification) ValidRange(chain.Rules) (int64, int64) {
    // Returning -1, -1 means that the action is always valid
    return -1, -1
}

// Helper functions
func verifySignature(data, signature, publicKey []byte) error {
    if len(signature) != ed25519.SignatureLen {
        return fmt.Errorf("invalid signature length: got %d, expected %d", len(signature), ed25519.SignatureLen)
    }
    if len(publicKey) != ed25519.PublicKeyLen {
        return fmt.Errorf("invalid public key length: got %d, expected %d", len(publicKey), ed25519.PublicKeyLen)
    }
    
    pub := ed25519.PublicKey(publicKey)
    return ed25519.Verify(pub, data, signature)
}

func calculateChecksum(code []byte) []byte {
    hash := sha256.Sum256(code)
    return hash[:]
}

func executeContract(code []byte) ([]byte, error) {
    // Implement contract execution logic here
    // This is a placeholder implementation
    results := sha256.Sum256(code)
    return results[:], nil
}

// Result type
type ContractVerificationResult struct {
    Success          bool   `serialize:"true" json:"success"`
    ExecutionResults []byte `serialize:"true" json:"execution_results"`
    Checksum        []byte `serialize:"true" json:"checksum"`
}

func (*ContractVerificationResult) GetTypeID() uint8 {
    return consts.ContractVerificationResultID
}
