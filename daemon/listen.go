package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

// UnixScheme is the addr prefix that selects a unix domain socket. An addr of
// "unix:///run/foo.sock" listens on that path; anything else is treated as a
// TCP host:port (":8080", "127.0.0.1:0").
const UnixScheme = "unix://"

// Listen builds the listener for addr. "unix://<path>" yields a unix domain
// socket (a stale socket file is removed first, the socket is chmod 0600, and
// it is unlinked on close); any other addr is a TCP listener. Local control
// planes should prefer a socket: the 0600 file perms are the access perimeter.
func Listen(addr string) (net.Listener, error) {
	if path, ok := strings.CutPrefix(addr, UnixScheme); ok {
		return listenUnix(path)
	}
	return net.Listen("tcp", addr)
}

func listenUnix(path string) (net.Listener, error) {
	// Remove a socket left behind by a crashed prior run; without this, bind
	// fails with "address already in use".
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket %s: %w", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(true)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod socket %s: %w", path, err)
	}
	return ln, nil
}
