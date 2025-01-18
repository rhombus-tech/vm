// cmd/compute/main.go
package main

import (
    "context"
    "flag"
    "log"
    "net"
    "os"
    "os/signal"
    "syscall"

    "google.golang.org/grpc"
    "github.com/rhombus-tech/vm/compute"
    "github.com/rhombus-tech/vm/tee/proto"
)

func main() {
    regionID := flag.String("region", "", "Region ID")
    port := flag.String("port", "50051", "Port to listen on")
    controllerPath := flag.String("controller", "", "Path to TEE controller")
    wasmPath := flag.String("wasm", "", "Path to WASM module")
    flag.Parse()

    if *regionID == "" {
        log.Fatal("region ID is required")
    }

    config := compute.DefaultConfig()
    config.ControllerPath = *controllerPath
    config.WasmPath = *wasmPath

    // Create root context for server lifecycle
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Create compute node without passing context
    node, err := compute.NewComputeNode(*regionID, config)
    if err != nil {
        log.Fatalf("Failed to create compute node: %v", err)
    }
    defer node.Close()

    lis, err := net.Listen("tcp", ":"+*port)
    if err != nil {
        log.Fatalf("Failed to listen: %v", err)
    }

    s := grpc.NewServer()
    proto.RegisterTeeExecutionServer(s, node)

    // Handle shutdown gracefully
    go func() {
        sigCh := make(chan os.Signal, 1)
        signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
        <-sigCh
        cancel() // Cancel context
        s.GracefulStop()
    }()

    // Use context to control server shutdown
    go func() {
        <-ctx.Done()
        s.GracefulStop()
    }()

    log.Printf("Compute node starting on port %s", *port)
    if err := s.Serve(lis); err != nil {
        if ctx.Err() != nil {
            log.Printf("Server stopped due to context cancellation")
        } else {
            log.Fatalf("Failed to serve: %v", err)
        }
    }
}