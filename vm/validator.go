package vm

import (
    "bytes"
    "encoding/binary"
    "errors"
)

var (
    ErrInvalidFormat     = errors.New("invalid code format")
    ErrUnsupportedFormat = errors.New("unsupported code format")
    ErrMalformedCode     = errors.New("malformed code")
    ErrCodeTooLarge      = errors.New("code exceeds size limit")
    ErrInvalidHeader     = errors.New("invalid code header")
)

const (
    // Code format identifiers
    FormatRaw    uint8 = 1
    FormatWasm   uint8 = 2
    FormatCustom uint8 = 3

    // Header magic bytes for verification
    HeaderMagic = "\x00SHUTTLE"
    HeaderSize  = 16 // Magic (8) + Format (1) + Version (1) + Reserved (6)
)

// CodeHeader represents the metadata for code
type CodeHeader struct {
    Format  uint8  // Code format identifier
    Version uint8  // Version number for the format
    Reserved [6]byte // Reserved for future use
}

// CodeValidator handles validation of code in different formats
type CodeValidator struct {
    maxSize uint64
    formats map[uint8]FormatValidator
}

// FormatValidator interface for different code formats
type FormatValidator interface {
    Validate(code []byte) error
}

// NewCodeValidator creates a new validator instance
func NewCodeValidator(maxSize uint64) *CodeValidator {
    cv := &CodeValidator{
        maxSize: maxSize,
        formats: make(map[uint8]FormatValidator),
    }

    // Register default format validators
    cv.RegisterFormat(FormatRaw, &RawValidator{})
    cv.RegisterFormat(FormatWasm, &WasmValidator{})
    cv.RegisterFormat(FormatCustom, &CustomValidator{})

    return cv
}

// ValidateCode validates code bytes and returns the format
func (cv *CodeValidator) ValidateCode(code []byte) error {
    // Check size
    if uint64(len(code)) > cv.maxSize {
        return ErrCodeTooLarge
    }

    // Must have at least a header
    if len(code) < HeaderSize {
        return ErrInvalidHeader
    }

    // Verify magic bytes
    if !bytes.Equal([]byte(code[:8]), []byte(HeaderMagic)) {
        return ErrInvalidHeader
    }

    // Parse header
    header := &CodeHeader{
        Format:  code[8],
        Version: code[9],
    }
    copy(header.Reserved[:], code[10:16])

    // Get validator for format
    validator, exists := cv.formats[header.Format]
    if !exists {
        return ErrUnsupportedFormat
    }

    // Validate format-specific code (after header)
    return validator.Validate(code[HeaderSize:])
}

// RegisterFormat registers a new format validator
func (cv *CodeValidator) RegisterFormat(format uint8, validator FormatValidator) {
    cv.formats[format] = validator
}

// Raw format validator (simple bytecode)
type RawValidator struct{}

func (v *RawValidator) Validate(code []byte) error {
    // Basic validation for raw format
    if len(code) == 0 {
        return ErrMalformedCode
    }
    
    // Verify basic structure (example)
    // - First byte: number of functions
    numFunctions := code[0]
    if len(code) < int(numFunctions)*2+1 { // minimum size check
        return ErrMalformedCode
    }
    
    return nil
}

// Wasm format validator
type WasmValidator struct{}

func (v *WasmValidator) Validate(code []byte) error {
    // Wasm magic number (\0asm)
    wasmMagic := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
    
    if len(code) < len(wasmMagic) {
        return ErrMalformedCode
    }
    
    if !bytes.Equal(code[:len(wasmMagic)], wasmMagic) {
        return ErrInvalidFormat
    }
    
    // TODO: Add more comprehensive WASM validation
    // This would include:
    // - Section validation
    // - Type checking
    // - Import/Export validation
    // - etc.
    
    return nil
}

// Custom format validator
type CustomValidator struct{}

func (v *CustomValidator) Validate(code []byte) error {
    // Example custom format validation
    if len(code) < 4 {
        return ErrMalformedCode
    }
    
    // Example: check for specific structure
    // - First 4 bytes: size of function table
    tableSize := binary.LittleEndian.Uint32(code[:4])
    if len(code) < int(tableSize)+4 {
        return ErrMalformedCode
    }
    
    return nil
}

// Helper function to create code with proper header
func CreateCode(format uint8, version uint8, code []byte) []byte {
    header := make([]byte, HeaderSize)
    copy(header, HeaderMagic)
    header[8] = format
    header[9] = version
    
    return append(header, code...)
}
