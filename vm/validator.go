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
    ErrInvalidTEEFormat  = errors.New("code format not supported by TEE")
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
    Format   uint8    // Code format identifier
    Version  uint8    // Version number for the format
    TEEType  uint8    // Type of TEE that can execute this code
    Reserved [5]byte  // Reserved for future use
}

// CodeValidator handles validation of code in different formats
type CodeValidator struct {
    maxSize uint64
    formats map[uint8]FormatValidator
    teeFormats map[uint8][]uint8 // Maps TEE types to supported formats
}

// FormatValidator interface for different code formats
type FormatValidator interface {
    Validate(code []byte) error
    ValidateForTEE(code []byte, teeType uint8) error
}

// NewCodeValidator creates a new validator instance
func NewCodeValidator(maxSize uint64) *CodeValidator {
    cv := &CodeValidator{
        maxSize: maxSize,
        formats: make(map[uint8]FormatValidator),
        teeFormats: make(map[uint8][]uint8),
    }

    // Register default format validators
    cv.RegisterFormat(FormatRaw, &RawValidator{})
    cv.RegisterFormat(FormatWasm, &WasmValidator{})
    cv.RegisterFormat(FormatCustom, &CustomValidator{})

    // Register TEE format support
    cv.RegisterTEEFormat(TEETypeSGX, []uint8{FormatWasm})
    cv.RegisterTEEFormat(TEETypeSEV, []uint8{FormatWasm, FormatCustom})

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
        TEEType: code[10],
    }
    copy(header.Reserved[:], code[11:16])

    // Get validator for format
    validator, exists := cv.formats[header.Format]
    if !exists {
        return ErrUnsupportedFormat
    }

    // Check if format is supported by TEE type
    if !cv.isFormatSupportedByTEE(header.Format, header.TEEType) {
        return ErrInvalidTEEFormat
    }

    // Validate format-specific code (after header)
    if err := validator.Validate(code[HeaderSize:]); err != nil {
        return err
    }

    // Validate TEE-specific requirements
    return validator.ValidateForTEE(code[HeaderSize:], header.TEEType)
}

// RegisterFormat registers a new format validator
func (cv *CodeValidator) RegisterFormat(format uint8, validator FormatValidator) {
    cv.formats[format] = validator
}

// RegisterTEEFormat registers which formats a TEE type supports
func (cv *CodeValidator) RegisterTEEFormat(teeType uint8, formats []uint8) {
    cv.teeFormats[teeType] = formats
}

func (cv *CodeValidator) isFormatSupportedByTEE(format uint8, teeType uint8) bool {
    supportedFormats, exists := cv.teeFormats[teeType]
    if !exists {
        return false
    }
    for _, f := range supportedFormats {
        if f == format {
            return true
        }
    }
    return false
}

// Raw format validator
type RawValidator struct{}

func (v *RawValidator) Validate(code []byte) error {
    if len(code) == 0 {
        return ErrMalformedCode
    }
    return nil
}

func (v *RawValidator) ValidateForTEE(code []byte, teeType uint8) error {
    // Raw format typically not supported in TEEs
    return ErrInvalidTEEFormat
}

// Wasm format validator
type WasmValidator struct{}

func (v *WasmValidator) Validate(code []byte) error {
    wasmMagic := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
    
    if len(code) < len(wasmMagic) {
        return ErrMalformedCode
    }
    
    if !bytes.Equal(code[:len(wasmMagic)], wasmMagic) {
        return ErrInvalidFormat
    }
    
    return nil
}

func (v *WasmValidator) ValidateForTEE(code []byte, teeType uint8) error {
    // Add TEE-specific WASM validation
    return nil
}

// Custom format validator
type CustomValidator struct{}

func (v *CustomValidator) Validate(code []byte) error {
    if len(code) < 4 {
        return ErrMalformedCode
    }
    
    tableSize := binary.LittleEndian.Uint32(code[:4])
    if len(code) < int(tableSize)+4 {
        return ErrMalformedCode
    }
    
    return nil
}

func (v *CustomValidator) ValidateForTEE(code []byte, teeType uint8) error {
    // Add TEE-specific custom format validation
    return nil
}

// Helper function to create code with proper header
func CreateCode(format uint8, version uint8, teeType uint8, code []byte) []byte {
    header := make([]byte, HeaderSize)
    copy(header, HeaderMagic)
    header[8] = format
    header[9] = version
    header[10] = teeType
    
    return append(header, code...)
}
