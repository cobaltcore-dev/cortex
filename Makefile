# Declare test targets as phony targets (not file names)
.PHONY: lint test-fast test-full

default: test-fast lint

# Run golangci-lint on all Go modules
lint:
	@echo "Running linters on all Go modules..."
	@for dir in */; do \
		if [ -f "$$dir/go.mod" ]; then \
			echo "Linting $$dir..."; \
			cd "$$dir" && golangci-lint run \
				--config ../.golangci.yaml \
			&& cd ..; \
		fi; \
	done

lint-fix:
	@echo "Running linters on all Go modules..."
	@for dir in */; do \
		if [ -f "$$dir/go.mod" ]; then \
			echo "Linting $$dir..."; \
			cd "$$dir" && golangci-lint run \
				--fix \
				--config ../.golangci.yaml \
			&& cd ..; \
		fi; \
	done

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