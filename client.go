package gosssd

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Client represents a connection to the SSSD NSS responder.
// NOTE: Client is not goroutine-safe; callers must serialize access
// or use separate Client instances per goroutine.
type Client struct {
	socketPath        string
	conn              net.Conn
	mu                sync.Mutex // guards conn and ctx watcher setup
	reqMu             sync.Mutex // serializes requests over the shared connection
	timeout           time.Duration
	ctx               context.Context
	ctxWatcherStarted bool
}

// ClientOption is a function that configures a Client
type ClientOption func(*Client)

// WithTimeout sets the timeout for socket operations
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.timeout = timeout
	}
}

// WithSocketPath sets a custom socket path
func WithSocketPath(path string) ClientOption {
	return func(c *Client) {
		c.socketPath = path
	}
}

// WithContext sets a context used to cancel in-flight operations.
func WithContext(ctx context.Context) ClientOption {
	return func(c *Client) {
		c.ctx = ctx
	}
}

// NewClient creates a new SSSD NSS client
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		socketPath: DefaultNSSSocketPath,
		timeout:    5 * time.Second,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Connect establishes a connection to the SSSD socket
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ctx != nil {
		if err := c.ctx.Err(); err != nil {
			return err
		}
	}

	if c.conn != nil {
		return nil // Already connected
	}

	// Prefer dialing with context when available; otherwise fall back to timeout.
	dialCtx := c.ctx
	if dialCtx == nil {
		dialCtx = context.Background()
	}

	var cancel context.CancelFunc
	if deadline, ok := dialCtx.Deadline(); !ok && c.timeout > 0 {
		// No deadline on the context; derive one from timeout for parity with DialTimeout.
		dialCtx, cancel = context.WithTimeout(dialCtx, c.timeout)
	} else if ok {
		// Ensure we don't hold onto a canceled context beyond this call.
		_ = deadline
	}
	if cancel != nil {
		defer cancel()
	}

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(dialCtx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", c.socketPath, err)
	}

	c.conn = conn
	if c.ctx != nil && !c.ctxWatcherStarted {
		c.ctxWatcherStarted = true
		go c.closeOnContextDone(c.ctx)
	}
	return nil
}

// ConnectContext establishes a connection using the provided context for dialing
// and for managing the lifecycle of the connection (socket will be closed when
// ctx is done). This is a convenience wrapper that avoids storing the context
// on the client.
func (c *Client) ConnectContext(ctx context.Context) error {
	return c.withContext(ctx).Connect()
}

// withContext returns a shallow copy of the client that uses the provided context.
// Internal helper to keep ConnectContext a thin wrapper without changing the
// original client's stored context.
func (c *Client) withContext(ctx context.Context) *Client {
	c.mu.Lock()
	// Create a new client with the same fields to avoid copying the mutex.
	clone := &Client{
		socketPath:        c.socketPath,
		conn:              c.conn,
		timeout:           c.timeout,
		ctx:               ctx,
		ctxWatcherStarted: false,
	}
	c.mu.Unlock()

	return clone
}

// Close closes the connection to SSSD
func (c *Client) Close() error {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if conn == nil {
		return nil
	}

	return conn.Close()
}

// sendRequest sends a request and receives a response
func (c *Client) sendRequest(command uint32, data []byte) (*Response, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	c.mu.Lock()
	conn := c.conn
	timeout := c.timeout
	ctx := c.ctx
	c.mu.Unlock()

	if conn == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Set write deadline
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("failed to set write deadline: %w", err)
	}

	// Marshal and send request
	req, err := MarshalRequest(command, data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	if _, err := conn.Write(req); err != nil {
		if ctx != nil && isConnClosed(err) {
			if cerr := ctx.Err(); cerr != nil {
				return nil, cerr
			}
		}
		return nil, fmt.Errorf("failed to write request: %w", err)
	}

	// Set read deadline
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("failed to set read deadline: %w", err)
	}

	// Read response header
	header := make([]byte, 16)
	if _, err := io.ReadFull(conn, header); err != nil {
		if ctx != nil && isConnClosed(err) {
			if cerr := ctx.Err(); cerr != nil {
				return nil, cerr
			}
		}
		return nil, fmt.Errorf("failed to read response header: %w", err)
	}

	length := binary.LittleEndian.Uint32(header[0:4])
	if length < 16 {
		return nil, fmt.Errorf("invalid response length: %d", length)
	}

	// Read response data
	fullResponse := make([]byte, length)
	copy(fullResponse, header)

	if length > 16 {
		if _, err := io.ReadFull(conn, fullResponse[16:]); err != nil {
			if ctx != nil && isConnClosed(err) {
				if cerr := ctx.Err(); cerr != nil {
					return nil, cerr
				}
			}
			return nil, fmt.Errorf("failed to read response data: %w", err)
		}
	}

	// Unmarshal response
	resp, err := UnmarshalResponse(fullResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Check response status
	if resp.Header.Status != SSS_NSS_STATUS_SUCCESS {
		return resp, fmt.Errorf("request failed with status: %d (len=%d, data_len=%d)", resp.Header.Status, resp.Header.Length, len(resp.Data))
	}

	return resp, nil
}

// GetUserByName looks up a user by username
func (c *Client) GetUserByName(username string) (*User, error) {
	data := MarshalString(username)

	resp, err := c.sendRequest(SSS_NSS_GETPWNAM, data)
	if err != nil {
		return nil, fmt.Errorf("GetUserByName failed: %w", err)
	}

	user, err := UnmarshalUser(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse user: %w", err)
	}

	return user, nil
}

// GetUserByUID looks up a user by UID
func (c *Client) GetUserByUID(uid uint32) (*User, error) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, uid)

	resp, err := c.sendRequest(SSS_NSS_GETPWUID, data)
	if err != nil {
		return nil, fmt.Errorf("GetUserByUID failed: %w", err)
	}

	user, err := UnmarshalUser(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse user: %w", err)
	}

	return user, nil
}

// GetGroupByName looks up a group by name
func (c *Client) GetGroupByName(groupname string) (*Group, error) {
	data := MarshalString(groupname)

	resp, err := c.sendRequest(SSS_NSS_GETGRNAM, data)
	if err != nil {
		return nil, fmt.Errorf("GetGroupByName failed: %w", err)
	}

	group, err := UnmarshalGroup(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse group: %w", err)
	}

	return group, nil
}

// GetGroupByGID looks up a group by GID
func (c *Client) GetGroupByGID(gid uint32) (*Group, error) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, gid)

	resp, err := c.sendRequest(SSS_NSS_GETGRGID, data)
	if err != nil {
		return nil, fmt.Errorf("GetGroupByGID failed: %w", err)
	}

	group, err := UnmarshalGroup(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse group: %w", err)
	}

	return group, nil
}

// GetGroupsForUser retrieves all groups that a user belongs to
// Protocol: INITGROUP Reply format
// 0-3: 32bit unsigned number of results
// 4-7: 32bit unsigned (reserved/padding)
// For each result: 0-3: 32bit number with gid
func (c *Client) GetGroupsForUser(username string) ([]uint32, error) {
	data := MarshalString(username)

	resp, err := c.sendRequest(SSS_NSS_INITGR, data)
	if err != nil {
		return nil, fmt.Errorf("GetGroupsForUser failed: %w", err)
	}

	// Parse response header
	if len(resp.Data) < 8 {
		return nil, fmt.Errorf("response too short for header: %d bytes", len(resp.Data))
	}

	// Number of GIDs
	count := binary.LittleEndian.Uint32(resp.Data[0:4])
	// Skip reserved field at 4-7

	gids := make([]uint32, count)
	offset := 8 // Skip header

	for i := uint32(0); i < count; i++ {
		if len(resp.Data) < offset+4 {
			return nil, fmt.Errorf("not enough data for GID %d", i)
		}
		gids[i] = binary.LittleEndian.Uint32(resp.Data[offset : offset+4])
		offset += 4
	}

	// Fetch primary GID from the user entry and ensure it's present
	user, err := c.GetUserByName(username)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user for primary group: %w", err)
	}

	primary := user.GID
	dedup := make(map[uint32]bool, len(gids)+1)
	for _, gid := range gids {
		dedup[gid] = true
	}
	if !dedup[primary] {
		gids = append(gids, primary)
	}

	return gids, nil
}

// closeOnContextDone closes the active connection when the client's context ends.
func (c *Client) closeOnContextDone(ctx context.Context) {
	<-ctx.Done()

	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
}

func isConnClosed(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe)
}
