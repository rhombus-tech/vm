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

// TEEPairConfig represents the configuration for a TEE pair
type TEEPairConfig struct {
    ID          string `json:"id"`
    SGXEndpoint string `json:"sgx_endpoint"`
    SEVEndpoint string `json:"sev_endpoint"`
    Status      string `json:"status"`
}

// TEEPairIdentity represents the core identification of a TEE pair
type TEEPairIdentity struct {
    SGXID []byte
    SEVID []byte
}

// TEEPairInfo represents complete information about a TEE pair
type TEEPairInfo struct {
    ID          string `json:"id"`
    SGXID       []byte `json:"sgx_id"`
    SEVID       []byte `json:"sev_id"`
    SGXEndpoint string `json:"sgx_endpoint"`
    SEVEndpoint string `json:"sev_endpoint"`
    Status      string `json:"status"`
}

// TEEPairConnection represents the client connections for a TEE pair
type TEEPairConnection struct {
    SGXClient proto.TeeExecutionClient
    SEVClient proto.TeeExecutionClient
    SGXConn   *grpc.ClientConn
    SEVConn   *grpc.ClientConn
}