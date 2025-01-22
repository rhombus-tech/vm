// interfaces/types.go
package interfaces

import (
	"time"

	"github.com/rhombus-tech/vm/tee/proto"
	"google.golang.org/grpc"
)

// Region represents a TEE execution region
type Region struct {
    ID        string    `json:"id"`
    CreatedAt time.Time `json:"created_at"`
    Workers   []string  `json:"worker_ids"`
    Status    string    `json:"status"`
}


// TEEPairIdentity represents the core identification of a TEE pair
type TEEPairIdentity struct {
    SGXID []byte
    SEVID []byte
}


// TEEPairConnection represents the client connections for a TEE pair
type TEEPairConnection struct {
    SGXClient proto.TeeExecutionClient
    SEVClient proto.TeeExecutionClient
    SGXConn   *grpc.ClientConn
    SEVConn   *grpc.ClientConn
}