package gosssd

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestMarshalRequest(t *testing.T) {
	tests := []struct {
		name    string
		command uint32
		data    []byte
		wantLen uint32
	}{
		{
			name:    "empty data",
			command: SSS_NSS_GETPWNAM,
			data:    []byte{},
			wantLen: 16,
		},
		{
			name:    "with data",
			command: SSS_NSS_GETPWUID,
			data:    []byte{1, 2, 3, 4},
			wantLen: 20,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := MarshalRequest(tt.command, tt.data)
			if err != nil {
				t.Fatalf("MarshalRequest() error = %v", err)
			}
			if uint32(len(req)) != tt.wantLen {
				t.Errorf("MarshalRequest() length = %d, want %d", len(req), tt.wantLen)
			}
			// Check header
			length := binary.LittleEndian.Uint32(req[0:4])
			if length != tt.wantLen {
				t.Errorf("Header length = %d, want %d", length, tt.wantLen)
			}
			cmd := binary.LittleEndian.Uint32(req[4:8])
			if cmd != tt.command {
				t.Errorf("Header command = %d, want %d", cmd, tt.command)
			}
			// Check data
			if len(tt.data) > 0 && !bytes.Equal(req[16:], tt.data) {
				t.Errorf("Data mismatch")
			}
		})
	}
}

func TestUnmarshalResponse(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		wantStatus uint32
		wantErr    bool
	}{
		{
			name: "valid response",
			data: func() []byte {
				buf := make([]byte, 20)
				binary.LittleEndian.PutUint32(buf[0:4], 20)  // length
				binary.LittleEndian.PutUint32(buf[4:8], 1)   // command
				binary.LittleEndian.PutUint32(buf[8:12], 0)  // status
				binary.LittleEndian.PutUint32(buf[12:16], 0) // reserved
				copy(buf[16:], []byte{1, 2, 3, 4})
				return buf
			}(),
			wantStatus: SSS_NSS_STATUS_SUCCESS,
			wantErr:    false,
		},
		{
			name:    "too short",
			data:    []byte{1, 2, 3},
			wantErr: true,
		},
		{
			name: "length mismatch",
			data: func() []byte {
				buf := make([]byte, 20)
				binary.LittleEndian.PutUint32(buf[0:4], 100) // wrong length
				return buf
			}(),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := UnmarshalResponse(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && resp.Header.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d", resp.Header.Status, tt.wantStatus)
			}
		})
	}
}

func TestMarshalString(t *testing.T) {
	tests := []struct {
		name    string
		str     string
		wantLen int
	}{
		{"empty", "", 1}, // just null terminator
		{"simple", "test", 5},
		{"with spaces", "hello world", 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := MarshalString(tt.str)
			if len(data) != tt.wantLen {
				t.Errorf("MarshalString() length = %d, want %d", len(data), tt.wantLen)
			}
			// Check null terminator
			if data[len(data)-1] != 0 {
				t.Errorf("Missing null terminator")
			}
			// Check string content
			if string(data[:len(data)-1]) != tt.str {
				t.Errorf("String content = %q, want %q", string(data[:len(data)-1]), tt.str)
			}
		})
	}
}

func TestUnmarshalUser(t *testing.T) {
	// Create a test user data buffer matching SSSD protocol:
	// 0-3: number of results
	// 4-7: reserved
	// 8-11: UID
	// 12-15: GID
	// 16+: 5 null-terminated strings (name, passwd, gecos, home, shell)
	buf := bytes.NewBuffer(nil)

	// Number of results
	numResults := make([]byte, 4)
	binary.LittleEndian.PutUint32(numResults, 1)
	buf.Write(numResults)

	// Reserved
	reserved := make([]byte, 4)
	buf.Write(reserved)

	// UID
	uidBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(uidBuf, 1000)
	buf.Write(uidBuf)

	// GID
	gidBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(gidBuf, 1000)
	buf.Write(gidBuf)

	// 5 null-terminated strings
	buf.Write(MarshalString("testuser"))
	buf.Write(MarshalString("x"))
	buf.Write(MarshalString("Test User"))
	buf.Write(MarshalString("/home/testuser"))
	buf.Write(MarshalString("/bin/bash"))

	user, err := UnmarshalUser(buf.Bytes())
	if err != nil {
		t.Fatalf("UnmarshalUser() error = %v", err)
	}
	if user.Name != "testuser" {
		t.Errorf("Name = %q, want %q", user.Name, "testuser")
	}
	if user.UID != 1000 {
		t.Errorf("UID = %d, want %d", user.UID, 1000)
	}
	if user.GID != 1000 {
		t.Errorf("GID = %d, want %d", user.GID, 1000)
	}
	if user.Gecos != "Test User" {
		t.Errorf("Gecos = %q, want %q", user.Gecos, "Test User")
	}
	if user.HomeDir != "/home/testuser" {
		t.Errorf("HomeDir = %q, want %q", user.HomeDir, "/home/testuser")
	}
	if user.Shell != "/bin/bash" {
		t.Errorf("Shell = %q, want %q", user.Shell, "/bin/bash")
	}
}

func TestUnmarshalGroup(t *testing.T) {
	// Create a test group data buffer matching SSSD protocol:
	// 0-3: number of results
	// 4-7: reserved
	// 8-11: GID
	// 12-15: number of members
	// 16+: null-terminated strings (name, passwd, then member names)
	buf := bytes.NewBuffer(nil)

	// Number of results
	numResults := make([]byte, 4)
	binary.LittleEndian.PutUint32(numResults, 1)
	buf.Write(numResults)

	// Reserved
	reserved := make([]byte, 4)
	buf.Write(reserved)

	// GID
	gidBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(gidBuf, 1000)
	buf.Write(gidBuf)

	// Member count
	memberCountBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(memberCountBuf, 2)
	buf.Write(memberCountBuf)

	// Null-terminated strings: name, passwd, then members
	buf.Write(MarshalString("testgroup"))
	buf.Write(MarshalString("x"))
	buf.Write(MarshalString("user1"))
	buf.Write(MarshalString("user2"))

	group, err := UnmarshalGroup(buf.Bytes())
	if err != nil {
		t.Fatalf("UnmarshalGroup() error = %v", err)
	}
	if group.Name != "testgroup" {
		t.Errorf("Name = %q, want %q", group.Name, "testgroup")
	}
	if group.GID != 1000 {
		t.Errorf("GID = %d, want %d", group.GID, 1000)
	}
	if len(group.Members) != 2 {
		t.Errorf("Member count = %d, want %d", len(group.Members), 2)
	}
	if len(group.Members) >= 2 {
		if group.Members[0] != "user1" {
			t.Errorf("Member[0] = %q, want %q", group.Members[0], "user1")
		}
		if group.Members[1] != "user2" {
			t.Errorf("Member[1] = %q, want %q", group.Members[1], "user2")
		}
	}
}
