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

	// retries is how many EXTRA attempts a request gets when the link to
	// SSSD is not working, and retryWait is the pause between them.
	retries   int
	retryWait time.Duration
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

// WithRetry sets how many extra attempts a request gets when the socket
// is unreachable, and how long to wait between them.
//
// The default is modest on purpose. SSSD is a local daemon whose socket
// routinely appears a moment after a process starts -- in a container the
// two start together -- and which is restarted by config reloads and by
// supervisors. Those are ordinary events, not failures a caller should
// have to code around.
//
// Zero attempts restores the previous behaviour: one try, and a dead
// connection stays dead.
func WithRetry(attempts int, wait time.Duration) ClientOption {
	return func(c *Client) {
		if attempts < 0 {
			attempts = 0
		}
		c.retries = attempts
		c.retryWait = wait
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
		retries:    2,
		retryWait:  100 * time.Millisecond,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Connect establishes a connection to the SSSD socket
func (c *Client) Connect() error {
	return c.connect(context.Background(), false)
}

// connect dials and stores the connection on THIS client.
//
// When override is true, ctx is used for dialing and for the lifecycle
// watcher in place of any context stored by WithContext. The boolean
// rather than a nil ctx keeps the two cases distinct: "no override" and
// "override with a background context" are different requests.
func (c *Client) connect(ctx context.Context, override bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// The effective context: an explicit one from ConnectContext, else
	// whatever WithContext stored.
	effCtx := c.ctx
	if override {
		effCtx = ctx
	}

	if effCtx != nil {
		if err := effCtx.Err(); err != nil {
			return err
		}
	}

	if c.conn != nil {
		return nil // Already connected
	}

	// Prefer dialing with context when available; otherwise fall back to timeout.
	dialCtx := effCtx
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
	if effCtx != nil && !c.ctxWatcherStarted {
		c.ctxWatcherStarted = true
		go c.closeOnContextDone(effCtx)
	}
	return nil
}

// ConnectContext establishes a connection using the provided context for dialing
// and for managing the lifecycle of the connection (socket will be closed when
// ctx is done). The context is not stored on the client.
//
// It connects THIS client. It previously dialed a shallow copy, so the
// connection was stored on a throwaway value and the caller's client was
// left with a nil conn: ConnectContext returned success and every request
// afterwards failed with "not connected".
func (c *Client) ConnectContext(ctx context.Context) error {
	return c.connect(ctx, true)
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

	// Connect on demand. A client built before SSSD was listening is the
	// normal case in a container, and it should not be permanently
	// useless because of when it happened to be constructed.
	if conn == nil {
		if err := c.connect(context.Background(), false); err != nil {
			return nil, err
		}
		c.mu.Lock()
		conn = c.conn
		c.mu.Unlock()
		if conn == nil {
			return nil, fmt.Errorf("not connected")
		}
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

// resetConn drops the current connection so the next attempt dials again.
func (c *Client) resetConn() {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// waitBeforeRetry pauses between attempts, and reports false if the
// client's context was cancelled while waiting.
func (c *Client) waitBeforeRetry() bool {
	c.mu.Lock()
	wait, ctx := c.retryWait, c.ctx
	c.mu.Unlock()

	if wait <= 0 {
		return ctx == nil || ctx.Err() == nil
	}
	if ctx == nil {
		time.Sleep(wait)
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// send runs a STATELESS request, reconnecting if the link has died.
//
// Only safe for requests that carry their whole meaning in one exchange.
// Anything that depends on per-connection state -- an enumeration cursor
// -- must not come through here: a reconnect would silently restart that
// state mid-sequence. See EnumerateUsers.
func (c *Client) send(command uint32, data []byte) (*Response, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			c.resetConn()
			if !c.waitBeforeRetry() {
				return nil, lastErr
			}
		}

		resp, err := c.sendRequest(command, data)
		if err == nil {
			return resp, nil
		}
		// A response means SSSD answered and the status IS its answer --
		// "no such user" is not a broken link and must not be retried.
		if resp != nil {
			return resp, err
		}
		lastErr = err

		c.mu.Lock()
		retries := c.retries
		c.mu.Unlock()
		if attempt >= retries {
			return nil, lastErr
		}
	}
}

// GetUserByName looks up a user by username
func (c *Client) GetUserByName(username string) (*User, error) {
	data := MarshalString(username)

	resp, err := c.send(SSS_NSS_GETPWNAM, data)
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

	resp, err := c.send(SSS_NSS_GETPWUID, data)
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

	resp, err := c.send(SSS_NSS_GETGRNAM, data)
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

	resp, err := c.send(SSS_NSS_GETGRGID, data)
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

	resp, err := c.send(SSS_NSS_INITGR, data)
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

// enumBatchSize is how many passwd entries one GETPWENT asks for.
//
// SSSD caps what it returns regardless, so this is an upper bound on the
// batch rather than a promise; it trades round trips against the size of a
// single reply.
const enumBatchSize = 100

// EnumerateUsers returns every user SSSD is willing to enumerate.
//
// This is SETPWENT / GETPWENT / ENDPWENT -- the same exchange getpwent(3)
// drives through the NSS module -- so it answers only when the SSSD domain
// sets `enumerate = true`. That is off by default and discouraged for large
// directories, and when it is off this returns no users rather than an
// error: SSSD simply reports the enumeration finished immediately, which is
// indistinguishable on the wire from a domain with nobody in it.
//
// Every entry is held in memory. For a directory of a few thousand accounts
// that is a few hundred kilobytes; for a very large one, prefer looking
// accounts up by name.
//
// A dropped connection restarts the whole enumeration rather than resuming
// it. The cursor lives on the connection, so resuming after a reconnect
// would silently re-read from the beginning and return duplicates, or skip
// what the old connection had already yielded -- a wrong answer reported as
// a successful one.
func (c *Client) EnumerateUsers() ([]*User, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			c.resetConn()
			if !c.waitBeforeRetry() {
				return nil, lastErr
			}
		}

		users, err, retryable := c.enumerateUsersOnce()
		if err == nil {
			return users, nil
		}
		lastErr = err
		if !retryable {
			return nil, lastErr
		}

		c.mu.Lock()
		retries := c.retries
		c.mu.Unlock()
		if attempt >= retries {
			return nil, lastErr
		}
	}
}

// enumerateUsersOnce runs one complete SETPWENT/GETPWENT/ENDPWENT sequence
// over a single connection. The third return reports whether the failure
// was the link rather than SSSD's answer, and so is worth another attempt.
func (c *Client) enumerateUsersOnce() ([]*User, error, bool) {
	if resp, err := c.sendRequest(SSS_NSS_SETPWENT, nil); err != nil {
		return nil, fmt.Errorf("SETPWENT failed: %w", err), resp == nil
	}
	// End the enumeration whatever happens: SSSD keeps per-connection state
	// for it, and abandoning that leaves the next caller reading from the
	// middle of this one's cursor.
	defer func() { _, _ = c.sendRequest(SSS_NSS_ENDPWENT, nil) }()

	var users []*User
	for {
		req := make([]byte, 4)
		binary.LittleEndian.PutUint32(req, enumBatchSize)

		resp, err := c.sendRequest(SSS_NSS_GETPWENT, req)
		if err != nil {
			return nil, fmt.Errorf("GETPWENT failed after %d users: %w", len(users), err), resp == nil
		}

		batch, err := UnmarshalUsers(resp.Data)
		if err != nil {
			// SSSD answered; the bytes are just not what we expected.
			// Another connection would produce the same thing.
			return nil, fmt.Errorf("parsing batch after %d users: %w", len(users), err), false
		}
		if len(batch) == 0 {
			// A zero count is how SSSD says the enumeration is complete.
			return users, nil, false
		}
		users = append(users, batch...)
	}
}
