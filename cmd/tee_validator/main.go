package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"gopkg.in/yaml.v2"
	"github.com/rhombus-tech/vm/verifier"
	"github.com/rhombus-tech/vm/vm"
)

type Config struct {
	Validator struct {
		Regions []struct {
			ID  string `yaml:"id"`
			SGX string `yaml:"sgx"`
			SEV string `yaml:"sev"`
		} `yaml:"regions"`
	} `yaml:"validator"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	// Read config
	configData, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("Failed to read config: %v", err)
	}

	var config Config
	if err := yaml.Unmarshal(configData, &config); err != nil {
		log.Fatalf("Failed to parse config: %v", err)
	}

	// Create validator with state verifier
	stateVerifier := &verifier.StateVerifier{}
	validator := vm.NewValidator(stateVerifier)

	// Add regions
	for _, region := range config.Validator.Regions {
		if err := validator.AddRegion(region.ID, region.SGX, region.SEV); err != nil {
			log.Fatalf("Failed to add region %s: %v", region.ID, err)
		}
		log.Printf("Added region %s (SGX: %s, SEV: %s)", region.ID, region.SGX, region.SEV)
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		fmt.Printf("\nReceived %v signal, shutting down...\n", sig)
		cancel()
	}()

	// Start validation loop
	log.Printf("Validator started, watching %d regions", len(config.Validator.Regions))
	<-ctx.Done()

	// Cleanup
	if err := validator.Close(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	log.Println("Validator stopped")
}
