# GoSSSD

A Go library for direct interaction with SSSD (System Security Services
Daemon) via its IPC protocol over Unix domain sockets. This allows Go programs
to do user/group lookups in Linux environments using SSSD (typically,
RHEL/Fedora variants) without needing to enable CGO and linking to glibc for
NSS support.

## Overview

GoSSSD provides a native Go client for communicating with SSSD's NSS (Name
Service Switch) responder, allowing you to query user and group information
without relying on CGO or libc NSS modules.

## Features

- Direct Unix socket communication with SSSD NSS responder
- User lookups by name or UID
- Group lookups by name or GID
- Group membership queries
- No CGO dependencies
- Client object is not goroutine-safe; guard with your own locking or use one
  client per goroutine
- Configurable timeouts, socket paths, and contexts
- Integration tests with self-contained SSSD build on Linux
- Mock server for cross-platform testing

## Installation

```bash
go get github.com/bbockelm/gosssd
```

## Usage

### Basic Example

```go
package main

import (
    "fmt"
    "log"

    "github.com/bbockelm/gosssd"
)

func main() {
    // Create a new client
    client := gosssd.NewClient()

    // Connect to SSSD
    if err := client.Connect(); err != nil {
        log.Fatalf("Failed to connect: %v", err)
    }
    defer client.Close()

    // Look up a user by name
    user, err := client.GetUserByName("testuser1")
    if err != nil {
        log.Fatalf("Failed to get user: %v", err)
    }

    fmt.Printf("User: %s (UID: %d, GID: %d)\n", user.Name, user.UID, user.GID)
    fmt.Printf("Home: %s, Shell: %s\n", user.HomeDir, user.Shell)
}
```

### Custom Configuration

```go
// Use custom socket path and timeout
client := gosssd.NewClient(
    gosssd.WithSocketPath("/var/lib/sss/pipes/nss"),
    gosssd.WithTimeout(10 * time.Second),
)
```

### Available Operations

#### User Lookups

```go
// By username
user, err := client.GetUserByName("username")

// By UID
user, err := client.GetUserByUID(1000)
```

#### Group Lookups

```go
// By group name
group, err := client.GetGroupByName("groupname")

// By GID
group, err := client.GetGroupByGID(1000)
```

#### Group Membership

```go
// Get all groups for a user
gids, err := client.GetGroupsForUser("username")
for _, gid := range gids {
    fmt.Printf("Member of GID: %d\n", gid)
}
```

## Development

### Prerequisites

- Go 1.24 or later
- Docker (for devcontainer)
- VS Code with Remote-Containers extension (recommended)

### Development Environment

This project includes a devcontainer configuration with AlmaLinux 9 and SSSD pre-configured:

1. Open the project in VS Code
2. Click "Reopen in Container" when prompted
3. The container will build and configure SSSD automatically

The devcontainer includes:

- Go 1.21+
- All necessary build tools

### Test Users

The devcontainer creates test users automatically:

- `testuser1` (UID: 10001)
- `testuser2` (UID: 10002)
- `testgroup` (GID: 10100) - contains both test users

However, tests should fallback cleanly to more common Linux users like
`nobody` if you're not in a container environment.

## Protocol Details

### SSSD NSS Protocol

The SSSD NSS responder uses a binary protocol over Unix domain sockets:

1. **Message Structure**: Length-prefixed messages with a 16-byte header
2. **Encoding**: Little-endian binary encoding
3. **Strings**: Length-prefixed with null terminators
4. **Socket**: Default path is `/var/lib/sss/pipes/nss`

### Message Format

```plaintext
Header (16 bytes):
- Length (4 bytes): Total message length including header
- Command (4 bytes): Request/response type
- Status (4 bytes): Response status code
- Reserved (4 bytes): Reserved for future use

Data (variable):
- Request/response payload
```

### Supported Commands

- `SSS_NSS_GETPWNAM`: Get user by name
- `SSS_NSS_GETPWUID`: Get user by UID
- `SSS_NSS_GETGRNAM`: Get group by name
- `SSS_NSS_GETGRGID`: Get group by GID
- `SSS_NSS_INITGR`: Get groups for user

## Architecture

```plaintext
┌─────────────┐
│  Your App   │
└──────┬──────┘
       │ gosssd.Client
       ▼
┌─────────────────────┐
│   Unix Socket       │
│ /var/lib/sss/pipes/ │
│      nss            │
└──────┬──────────────┘
       │
       ▼
┌─────────────────────┐
│  SSSD NSS Responder │
└──────┬──────────────┘
       │
       ▼
┌─────────────────────┐
│  Identity Provider  │
│  (LDAP/AD/IPA/etc)  │
└─────────────────────┘
```

## Limitations

- Currently implements only NSS protocol (no PAM, SSH, sudo responders).
  Only does the user/group lookups, not hosts.
- No caching (relies on SSSD's caching)
- Requires SSSD to be running and properly configured

## Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch
3. Add tests for new functionality
4. Ensure all tests pass
5. Submit a pull request

## References

- [SSSD Documentation](https://sssd.io/)
- [SSSD NSS Protocol](https://github.com/SSSD/sssd/blob/master/src/responder/nss/nss_protocol.h)
- [SSSD Passwd Protocol](https://github.com/SSSD/sssd/blob/master/src/sss_client/nss_passwd.c)
- [SSSD Group Protocol](https://github.com/SSSD/sssd/blob/master/src/sss_client/nss_group.c)
- [Unix Domain Sockets](https://man7.org/linux/man-pages/man7/unix.7.html)

## Acknowledgments

This library implements the SSSD NSS responder protocol based on the SSSD
project's specifications.
