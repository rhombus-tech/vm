// interfaces/iterator.go
package interfaces

type Iterator interface {
    Next() bool
    Key() []byte
    Value() []byte
    Error() error
    Close()
}