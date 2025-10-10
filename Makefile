# Declare test targets as phony targets (not file names)
.PHONY: test test-fast test-full

# Default test command - runs without Docker (fast)
test: test-fast

# Run tests without Docker/PostgreSQL container (faster, but some tests may be skipped)
test-fast:
	@echo "Running tests without PostgreSQL container..."
	@for dir in */; do \
		if [ -f "$$dir/go.mod" ]; then \
			echo "Testing in $$dir..."; \
			cd "$$dir" && POSTGRES_CONTAINER=0 go test ./... && cd ..; \
		fi; \
	done

# Run tests with Docker/PostgreSQL container (slower, but runs all tests)
test-full:
	@echo "Running tests with PostgreSQL container..."
	@for dir in */; do \
		if [ -f "$$dir/go.mod" ]; then \
			echo "Testing in $$dir..."; \
			cd "$$dir" && POSTGRES_CONTAINER=1 go test ./... && cd ..; \
		fi; \
	done