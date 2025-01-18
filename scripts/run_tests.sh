// scripts/run_tests.sh
#!/bin/bash

# Start test environment if needed
docker-compose up -d devnet

# Wait for node to be ready
echo "Waiting for node to be ready..."
sleep 10

# Run tests with environment variable
TEST_VM_ENDPOINT=devnet:9650 go test -v ./tests/vm/...

# Cleanup
docker-compose down