# Agent Instructions for GoSSSD Project

## Project Overview

GoSSSD is a Go library that provides direct interaction with SSSD
(System Security Services Daemon) via its IPC protocol over Unix
domain sockets. The project allows querying user and group information
without relying on CGO or libc NSS modules.

**Repository:** github.com/bbockelm/gosssd  
**Language:** Go 1.21+  
**License:** See LICENSE file

## Project Structure

```plaintext
gosssd/
├── build_sssd.go      # Helper for building a copy of SSSD for testing
├── client.go          # Main client implementation for SSSD communication
├── protocol.go        # SSSD protocol definitions and message handling
├── protocol_test.go   # Protocol tests
├── go.mod            # Go module definition
├── cmd/
│   └── gosssd-cli/   # Command-line interface tool
│       └── main.go
├── .github/
│   └── workflows/    # CI/CD workflows
└── .pre-commit-config.yaml  # Pre-commit hooks configuration
```

## Key Features

- Unix socket communication with SSSD NSS responder
- User lookups by name or UID
- Group lookups by name or GID
- Group membership queries
- No CGO dependencies
- Thread-safe client implementation
- Configurable timeouts and socket paths

## Development Guidelines

### Code Quality Standards

1. **Code Style**
   - Follow standard Go formatting (gofmt)
   - Use go vet for static analysis
   - Adhere to golangci-lint recommendations, even in tests
   - Write idiomatic Go code
   - Run pre-commit before committing code

2. **Testing**
   - Write unit tests for new functionality
   - Maintain or improve code coverage
   - Use table-driven tests where appropriate
   - Run tests with race detector: `go test -race ./...`
   - Include integration tests that use the sssd built for testing

3. **Documentation**
   - Add godoc comments for exported functions/types
   - Keep README.md up to date with examples
   - Document non-obvious implementation details

### Pre-commit Hooks

The project uses pre-commit hooks to ensure code quality. To set up:

```bash
pip install pre-commit
pre-commit install
```

Pre-commit will automatically run:

- Code formatting (gofmt)
- Static analysis (go vet, staticcheck)
- Security checks (gosec)
- Tests with race detector
- YAML/Markdown linting

### CI/CD Workflows

The project includes four GitHub Actions workflows:

1. **pre-commit.yml** - Runs all pre-commit hooks
2. **golangci-lint.yml** - Comprehensive Go linting
3. **build.yml** - Builds the library and CLI on Go 1.21, 1.22, and 1.23
4. **test.yml** - Runs tests with coverage reporting

All workflows trigger on pull requests and pushes to main/master branches.

### Building

Build the library:

```bash
go build ./...
```

Build the CLI tool:

```bash
go build -o gosssd-cli ./cmd/gosssd-cli
```

### Testing

Run all tests:

```bash
go test -v ./...
```

Run tests with race detection and coverage:

```bash
go test -v -race -coverprofile=coverage.txt -covermode=atomic ./...
```

## Common Tasks

### Adding New Features

1. Create feature branch from main
2. Implement feature with tests
3. Run pre-commit checks: `pre-commit run --all-files`
4. Ensure all tests pass locally
5. Create pull request with clear description
6. Wait for CI/CD checks to pass

### Fixing Bugs

1. Add failing test case that reproduces the bug
2. Implement fix
3. Verify test passes
4. Run full test suite with race detector
5. Submit pull request

### Updating Dependencies

1. Run `go get -u ./...` to update dependencies
2. Run `go mod tidy` to clean up
3. Test thoroughly to ensure compatibility
4. Commit go.mod and go.sum changes

## Technical Context

### SSSD Protocol

The library implements the SSSD NSS responder protocol, which communicates
over Unix domain sockets (typically `/var/lib/sss/pipes/nss`). The
protocol uses a binary message format with specific request/response
structures.

### Key Components

- **Client**: Manages connection lifecycle and request/response handling
- **Protocol**: Defines message structures and serialization
- **NSS Operations**: Implements user/group lookup operations

### Thread Safety

The client implementation is designed to be thread-safe. Multiple goroutines
can use the same client instance concurrently.  Use
contexts in interfaces to allow users to timeout

## Best Practices

1. **Error Handling**: Return meaningful errors with context
2. **Timeouts**: Use appropriate timeouts for socket operations
3. **Resource Management**: Always close connections and clean up resources
4. **Logging**: Use structured logging for debugging (when appropriate)
5. **Versioning**: Follow semantic versioning for releases

## Contact & Support

For questions or issues:

- Open a GitHub issue
- Check existing issues and pull requests
- Refer to SSSD documentation for protocol details

## Agent-Specific Notes

When working with this codebase:

- The project has no CGO dependencies - keep it that way
- Protocol implementation must match SSSD's expectations exactly
- Test against real SSSD installations when possible
- Consider backward compatibility with different SSSD versions
- Socket communication is low-level - be careful with buffer handling
- Performance matters - avoid unnecessary allocations in hot paths
