// interfaces/types.go
package interfaces

import (
	"time"

	"github.com/rhombus-tech/vm/tee/proto"
	"google.golang.org/grpc"
)

// Region represents a TEE execution region
type Region struct {
    ID            string                `json:"id"`
    Workers       [2]string            `json:"workers"`
    Status        string               `json:"status"`
    CreatedAt     time.Time           `json:"created_at"`
    LastUpdated   time.Time           `json:"last_updated"`
    MaxObjects    int                 `json:"max_objects"`
    MaxEvents     int                 `json:"max_events"`
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