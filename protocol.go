package gosssd

import (
	"encoding/binary"
	"fmt"
	"os"
)

// SSSD NSS protocol constants
const (
	// Default socket paths
	DefaultNSSSocketPath = "/var/lib/sss/pipes/nss"

	// Protocol version
	ProtocolVersion = 1

	// Request commands (based on SSSD NSS responder protocol)
	// From src/sss_client/sss_cli.h
	SSS_GET_VERSION       = 0x0001 // Get protocol version
	SSS_NSS_GETPWNAM      = 0x0011 // Get user by name
	SSS_NSS_GETPWUID      = 0x0012 // Get user by UID
	SSS_NSS_SETPWENT      = 0x0013 // Begin user enumeration
	SSS_NSS_GETPWENT      = 0x0014 // Get next user entry
	SSS_NSS_ENDPWENT      = 0x0015 // End user enumeration
	SSS_NSS_GETGRNAM      = 0x0021 // Get group by name
	SSS_NSS_GETGRGID      = 0x0022 // Get group by GID
	SSS_NSS_SETGRENT      = 0x0023 // Begin group enumeration
	SSS_NSS_GETGRENT      = 0x0024 // Get next group entry
	SSS_NSS_ENDGRENT      = 0x0025 // End group enumeration
	SSS_NSS_INITGR        = 0x0026 // Get groups for user
	SSS_NSS_SETNETGRENT   = 0x0061 // Begin netgroup enumeration
	SSS_NSS_GETNETGRENT   = 0x0062 // Get next netgroup entry
	SSS_NSS_ENDNETGRENT   = 0x0063 // End netgroup enumeration
	SSS_NSS_GETSERVBYNAME = 0x00A1 // Get service by name
	SSS_NSS_GETSERVBYPORT = 0x00A2 // Get service by port
	SSS_NSS_SETSERVENT    = 0x00A3 // Begin service enumeration
	SSS_NSS_GETSERVENT    = 0x00A4 // Get next service entry
	SSS_NSS_ENDSERVENT    = 0x00A5 // End service enumeration

	// Response status codes
	SSS_NSS_STATUS_SUCCESS        = 0
	SSS_NSS_STATUS_NOTFOUND       = 1
	SSS_NSS_STATUS_UNAVAIL        = 2
	SSS_NSS_STATUS_TRYAGAIN       = 3
	SSS_NSS_STATUS_PROTOCOL_ERROR = 4
)

// MessageHeader represents the SSSD protocol message header
// The protocol uses a simple TLV (Type-Length-Value) format
type MessageHeader struct {
	Length   uint32 // Total message length including header
	Command  uint32 // Command/request type
	Status   uint32 // Response status (for responses)
	Reserved uint32 // Reserved for future use
}

// Request represents a generic SSSD NSS request
type Request struct {
	Header MessageHeader
	Data   []byte
}

// Response represents a generic SSSD NSS response
type Response struct {
	Header MessageHeader
	Data   []byte
}

// User represents a passwd entry returned by SSSD
type User struct {
	Name    string
	Passwd  string
	UID     uint32
	GID     uint32
	Gecos   string
	HomeDir string
	Shell   string
}

// Group represents a group entry returned by SSSD
type Group struct {
	Name    string
	Passwd  string
	GID     uint32
	Members []string
}

// MarshalRequest creates a binary request message
func MarshalRequest(command uint32, data []byte) ([]byte, error) {
	headerSize := uint32(16)                   // 4 fields × 4 bytes
	totalLen := headerSize + uint32(len(data)) //#nosec G115  // len(data) is bounded

	buf := make([]byte, totalLen)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(totalLen)) // #nosec G115 - totalLen is bounded by message size
	binary.LittleEndian.PutUint32(buf[4:8], command)
	binary.LittleEndian.PutUint32(buf[8:12], 0)  // Status (unused in request)
	binary.LittleEndian.PutUint32(buf[12:16], 0) // Reserved

	if len(data) > 0 {
		copy(buf[16:], data)
	}

	return buf, nil
}

// UnmarshalResponse parses a binary response message
func UnmarshalResponse(data []byte) (*Response, error) {
	if len(data) < 16 {
		return nil, fmt.Errorf("response too short: %d bytes", len(data))
	}

	resp := &Response{
		Header: MessageHeader{
			Length:   binary.LittleEndian.Uint32(data[0:4]),
			Command:  binary.LittleEndian.Uint32(data[4:8]),
			Status:   binary.LittleEndian.Uint32(data[8:12]),
			Reserved: binary.LittleEndian.Uint32(data[12:16]),
		},
		Data: data[16:],
	}

	if int(resp.Header.Length) != len(data) {
		return nil, fmt.Errorf("length mismatch: header=%d, actual=%d", resp.Header.Length, len(data))
	}

	return resp, nil
}

// MarshalString creates a null-terminated string for protocol messages
// For GETPWNAM requests, it's just the username as a null-terminated string
func MarshalString(s string) []byte {
	buf := make([]byte, len(s)+1) // string + null terminator
	copy(buf, s)
	buf[len(s)] = 0
	return buf
}

// UnmarshalString reads a length-prefixed string from data
func UnmarshalString(data []byte, offset int) (string, int, error) {
	if len(data) < offset+4 {
		return "", offset, fmt.Errorf("not enough data for string length")
	}

	slen := binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	if len(data) < offset+int(slen) {
		return "", offset, fmt.Errorf("not enough data for string content")
	}

	// Remove null terminator if present
	end := offset + int(slen)
	if slen > 0 && data[end-1] == 0 {
		end--
	}

	str := string(data[offset:end])
	offset += int(slen)

	return str, offset, nil
}

// UnmarshalUser parses user data from a response
// Protocol format (from nss_passwd.c):
// 0-3: 32bit unsigned number of results
// 4-7: 32bit unsigned (reserved/padding)
// For each result:
//
//	0-3: 32bit number uid
//	4-7: 32bit number gid
//	8-X: sequence of 5, 0 terminated, strings (name, passwd, gecos, dir, shell)
func UnmarshalUser(data []byte) (*User, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("response too short for header: %d bytes", len(data))
	}

	// Parse number of results
	numResults := binary.LittleEndian.Uint32(data[0:4])
	if numResults == 0 {
		return nil, fmt.Errorf("no results found")
	}
	if numResults > 1 {
		// Just use the first result
		fmt.Fprintf(os.Stderr, "[DEBUG] Multiple results (%d), using first\n", numResults)
	}

	// Skip reserved field
	offset := 8

	user := &User{}

	// Parse UID
	if len(data) < offset+4 {
		return nil, fmt.Errorf("not enough data for UID")
	}
	user.UID = binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Parse GID
	if len(data) < offset+4 {
		return nil, fmt.Errorf("not enough data for GID")
	}
	user.GID = binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Parse the 5 null-terminated strings: name, passwd, gecos, dir, shell
	var err error

	user.Name, offset, err = unmarshalCString(data, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to parse name: %w", err)
	}

	user.Passwd, offset, err = unmarshalCString(data, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to parse passwd: %w", err)
	}

	user.Gecos, offset, err = unmarshalCString(data, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to parse gecos: %w", err)
	}

	user.HomeDir, offset, err = unmarshalCString(data, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to parse home dir: %w", err)
	}

	var shellOffset int
	user.Shell, shellOffset, err = unmarshalCString(data, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to parse shell: %w", err)
	}
	_ = shellOffset // offset is used for potential future parsing

	return user, nil
}

// unmarshalCString reads a null-terminated C string from data at offset
func unmarshalCString(data []byte, offset int) (string, int, error) {
	if offset >= len(data) {
		return "", offset, fmt.Errorf("offset %d beyond data length %d", offset, len(data))
	}

	// Find null terminator
	end := offset
	for end < len(data) && data[end] != 0 {
		end++
	}

	if end >= len(data) {
		return "", offset, fmt.Errorf("no null terminator found")
	}

	str := string(data[offset:end])
	return str, end + 1, nil // Skip past null terminator
}

// UnmarshalGroup parses group data from a response
// Protocol format (from nss_group.c):
// 0-3: 32bit unsigned number of results
// 4-7: 32bit unsigned (reserved/padding)
// For each result:
//
//	0-3: 32bit number gid
//	4-7: 32bit unsigned number of members
//	8-X: sequence of 0 terminated strings (name, passwd, members...)
func UnmarshalGroup(data []byte) (*Group, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("response too short for header: %d bytes", len(data))
	}

	// Parse number of results
	numResults := binary.LittleEndian.Uint32(data[0:4])
	if numResults == 0 {
		return nil, fmt.Errorf("no results found")
	}
	if numResults > 1 {
		fmt.Fprintf(os.Stderr, "[DEBUG] Multiple results (%d), using first\n", numResults)
	}

	// Skip reserved field
	offset := 8

	group := &Group{}

	// Parse GID
	if len(data) < offset+4 {
		return nil, fmt.Errorf("not enough data for GID")
	}
	group.GID = binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Parse member count
	if len(data) < offset+4 {
		return nil, fmt.Errorf("not enough data for member count")
	}
	memberCount := binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Parse name
	var err error
	group.Name, offset, err = unmarshalCString(data, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to parse name: %w", err)
	}

	// Parse passwd
	group.Passwd, offset, err = unmarshalCString(data, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to parse passwd: %w", err)
	}

	// Parse members
	group.Members = make([]string, memberCount)
	for i := uint32(0); i < memberCount; i++ {
		group.Members[i], offset, err = unmarshalCString(data, offset)
		if err != nil {
			return nil, fmt.Errorf("failed to parse member %d: %w", i, err)
		}
	}

	return group, nil
}
