package gosssd

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// MockServer implements a mock SSSD NSS server for testing.
// This allows tests to run on any platform, not just Linux with SSSD.
type MockServer struct {
	socketPath  string
	listener    net.Listener
	users       map[string]*User
	usersByUID  map[uint32]*User
	groups      map[string]*Group
	groupsByGID map[uint32]*Group
	userGroups  map[string][]uint32 // username -> GIDs
	mu          sync.RWMutex
	done        chan struct{}
	wg          sync.WaitGroup
}

// NewMockServer creates a new mock SSSD server.
func NewMockServer(socketPath string) (*MockServer, error) {
	// Remove socket if it exists
	_ = os.Remove(socketPath)

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create socket directory: %w", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on socket: %w", err)
	}

	s := &MockServer{
		socketPath:  socketPath,
		listener:    listener,
		users:       make(map[string]*User),
		usersByUID:  make(map[uint32]*User),
		groups:      make(map[string]*Group),
		groupsByGID: make(map[uint32]*Group),
		userGroups:  make(map[string][]uint32),
		done:        make(chan struct{}),
	}

	s.wg.Add(1)
	go s.serve()

	return s, nil
}

// AddUser adds a mock user to the server.
func (s *MockServer) AddUser(user *User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[user.Name] = user
	s.usersByUID[user.UID] = user
}

// AddGroup adds a mock group to the server.
func (s *MockServer) AddGroup(group *Group) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups[group.Name] = group
	s.groupsByGID[group.GID] = group
}

// SetUserGroups sets the group memberships for a user.
func (s *MockServer) SetUserGroups(username string, gids []uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userGroups[username] = gids
}

// Close shuts down the mock server.
func (s *MockServer) Close() error {
	close(s.done)
	_ = s.listener.Close()
	s.wg.Wait()
	_ = os.Remove(s.socketPath)
	return nil
}

func (s *MockServer) serve() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConnection(conn)
		}()
	}
}

func (s *MockServer) handleConnection(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	for {
		// Read request header
		header := make([]byte, 16)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}

		length := binary.LittleEndian.Uint32(header[0:4])
		command := binary.LittleEndian.Uint32(header[4:8])

		// Read request data
		dataLen := length - 16
		data := make([]byte, dataLen)
		if dataLen > 0 {
			if _, err := io.ReadFull(conn, data); err != nil {
				return
			}
		}

		// Handle request
		response := s.handleRequest(command, data)
		if response != nil {
			_, _ = conn.Write(response)
		}
	}
}

func (s *MockServer) handleRequest(command uint32, data []byte) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()

	switch command {
	case SSS_GET_VERSION:
		return s.respondVersion()

	case SSS_NSS_GETPWNAM:
		username := string(data[:len(data)-1]) // Remove null terminator
		return s.respondGetUserByName(username)

	case SSS_NSS_GETPWUID:
		if len(data) < 4 {
			return s.respondError(SSS_NSS_STATUS_UNAVAIL)
		}
		uid := binary.LittleEndian.Uint32(data[0:4])
		return s.respondGetUserByUID(uid)

	case SSS_NSS_GETGRNAM:
		groupname := string(data[:len(data)-1])
		return s.respondGetGroupByName(groupname)

	case SSS_NSS_GETGRGID:
		if len(data) < 4 {
			return s.respondError(SSS_NSS_STATUS_UNAVAIL)
		}
		gid := binary.LittleEndian.Uint32(data[0:4])
		return s.respondGetGroupByGID(gid)

	case SSS_NSS_INITGR:
		username := string(data[:len(data)-1])
		return s.respondGetGroupsForUser(username)

	default:
		return s.respondError(SSS_NSS_STATUS_UNAVAIL)
	}
}

func (s *MockServer) respondVersion() []byte {
	// Response: header + version (uint32)
	resp := make([]byte, 20)
	binary.LittleEndian.PutUint32(resp[0:4], 20)                        // length
	binary.LittleEndian.PutUint32(resp[4:8], SSS_GET_VERSION)           // command
	binary.LittleEndian.PutUint32(resp[8:12], SSS_NSS_STATUS_SUCCESS)   // status
	binary.LittleEndian.PutUint32(resp[12:16], 0)                       // reserved
	binary.LittleEndian.PutUint32(resp[16:20], uint32(ProtocolVersion)) // version
	return resp
}

func (s *MockServer) respondError(status uint32) []byte {
	resp := make([]byte, 16)
	binary.LittleEndian.PutUint32(resp[0:4], 16)      // length
	binary.LittleEndian.PutUint32(resp[4:8], 0)       // command
	binary.LittleEndian.PutUint32(resp[8:12], status) // status
	binary.LittleEndian.PutUint32(resp[12:16], 0)     // reserved
	return resp
}

func (s *MockServer) respondGetUserByName(username string) []byte {
	user, ok := s.users[username]
	if !ok {
		return s.respondError(SSS_NSS_STATUS_NOTFOUND)
	}
	return s.marshalUserResponse(user)
}

func (s *MockServer) respondGetUserByUID(uid uint32) []byte {
	user, ok := s.usersByUID[uid]
	if !ok {
		return s.respondError(SSS_NSS_STATUS_NOTFOUND)
	}
	return s.marshalUserResponse(user)
}

func (s *MockServer) marshalUserResponse(user *User) []byte {
	// Calculate data size
	dataSize := 8 + // num_results + reserved
		8 + // uid + gid
		len(user.Name) + 1 +
		len(user.Passwd) + 1 +
		len(user.Gecos) + 1 +
		len(user.HomeDir) + 1 +
		len(user.Shell) + 1

	resp := make([]byte, 16+dataSize)

	// Header
	binary.LittleEndian.PutUint32(resp[0:4], uint32(16+dataSize))     // length
	binary.LittleEndian.PutUint32(resp[4:8], SSS_NSS_GETPWNAM)        // command
	binary.LittleEndian.PutUint32(resp[8:12], SSS_NSS_STATUS_SUCCESS) // status
	binary.LittleEndian.PutUint32(resp[12:16], 0)                     // reserved

	// Data
	offset := 16
	binary.LittleEndian.PutUint32(resp[offset:offset+4], 1) // num_results
	offset += 4
	binary.LittleEndian.PutUint32(resp[offset:offset+4], 0) // reserved
	offset += 4
	binary.LittleEndian.PutUint32(resp[offset:offset+4], user.UID)
	offset += 4
	binary.LittleEndian.PutUint32(resp[offset:offset+4], user.GID)
	offset += 4

	offset += copy(resp[offset:], user.Name)
	resp[offset] = 0
	offset++

	offset += copy(resp[offset:], user.Passwd)
	resp[offset] = 0
	offset++

	offset += copy(resp[offset:], user.Gecos)
	resp[offset] = 0
	offset++

	offset += copy(resp[offset:], user.HomeDir)
	resp[offset] = 0
	offset++

	offset += copy(resp[offset:], user.Shell)
	resp[offset] = 0

	return resp
}

func (s *MockServer) respondGetGroupByName(groupname string) []byte {
	group, ok := s.groups[groupname]
	if !ok {
		return s.respondError(SSS_NSS_STATUS_NOTFOUND)
	}
	return s.marshalGroupResponse(group)
}

func (s *MockServer) respondGetGroupByGID(gid uint32) []byte {
	group, ok := s.groupsByGID[gid]
	if !ok {
		return s.respondError(SSS_NSS_STATUS_NOTFOUND)
	}
	return s.marshalGroupResponse(group)
}

func (s *MockServer) marshalGroupResponse(group *Group) []byte {
	// Calculate data size
	dataSize := 8 + // num_results + reserved
		8 + // gid + member_count
		len(group.Name) + 1 +
		len(group.Passwd) + 1

	for _, member := range group.Members {
		dataSize += len(member) + 1
	}

	resp := make([]byte, 16+dataSize)

	// Header
	binary.LittleEndian.PutUint32(resp[0:4], uint32(16+dataSize))     // length
	binary.LittleEndian.PutUint32(resp[4:8], SSS_NSS_GETGRNAM)        // command
	binary.LittleEndian.PutUint32(resp[8:12], SSS_NSS_STATUS_SUCCESS) // status
	binary.LittleEndian.PutUint32(resp[12:16], 0)                     // reserved

	// Data
	offset := 16
	binary.LittleEndian.PutUint32(resp[offset:offset+4], 1) // num_results
	offset += 4
	binary.LittleEndian.PutUint32(resp[offset:offset+4], 0) // reserved
	offset += 4
	binary.LittleEndian.PutUint32(resp[offset:offset+4], group.GID)
	offset += 4
	binary.LittleEndian.PutUint32(resp[offset:offset+4], uint32(len(group.Members)))
	offset += 4

	offset += copy(resp[offset:], group.Name)
	resp[offset] = 0
	offset++

	offset += copy(resp[offset:], group.Passwd)
	resp[offset] = 0
	offset++

	for _, member := range group.Members {
		offset += copy(resp[offset:], member)
		resp[offset] = 0
		offset++
	}

	return resp
}

func (s *MockServer) respondGetGroupsForUser(username string) []byte {
	gids, ok := s.userGroups[username]
	if !ok {
		// Return empty list
		gids = []uint32{}
	}

	dataSize := 8 + len(gids)*4 // num_results + reserved + gids
	resp := make([]byte, 16+dataSize)

	// Header
	binary.LittleEndian.PutUint32(resp[0:4], uint32(16+dataSize))     // length
	binary.LittleEndian.PutUint32(resp[4:8], SSS_NSS_INITGR)          // command
	binary.LittleEndian.PutUint32(resp[8:12], SSS_NSS_STATUS_SUCCESS) // status
	binary.LittleEndian.PutUint32(resp[12:16], 0)                     // reserved

	// Data
	offset := 16
	binary.LittleEndian.PutUint32(resp[offset:offset+4], uint32(len(gids))) // num_results
	offset += 4
	binary.LittleEndian.PutUint32(resp[offset:offset+4], 0) // reserved
	offset += 4

	for _, gid := range gids {
		binary.LittleEndian.PutUint32(resp[offset:offset+4], gid)
		offset += 4
	}

	return resp
}
