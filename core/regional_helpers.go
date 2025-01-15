// core/regional_helpers.go
package core

import (
    "bytes"
)

// MakeRegionalKey creates a properly formatted regional key
func MakeRegionalKey(regionID string, key []byte) []byte {
    return bytes.Join([][]byte{
        []byte("r"),
        []byte(regionID),
        key,
    }, []byte("/"))
}

// ParseRegionalKey extracts region ID and original key
func ParseRegionalKey(key []byte) (string, []byte, error) {
    parts := bytes.Split(key, []byte("/"))
    if len(parts) < 3 || !bytes.Equal(parts[0], []byte("r")) {
        return "", nil, ErrInvalidRegionalKey
    }
    
    return string(parts[1]), parts[2], nil
}

// ValidateRegionID performs basic validation of region IDs
func ValidateRegionID(id string) error {
    if id == "" {
        return ErrInvalidRegionID
    }
    // Add additional validation rules as needed
    return nil
}