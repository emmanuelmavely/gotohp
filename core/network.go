package core

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// isNetworkError reports whether an upload failure looks like a connectivity
// problem rather than a server-side rejection, so a paused run can wait for
// the network instead of counting the file as failed.
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection reset",
		"connection refused",
		"broken pipe",
		"no such host",
		"network is unreachable",
		"network is down",
		"dial tcp",
		"dial udp",
		"proxyconnect",
		"unexpected eof",
		"tls handshake",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// connectivityProbeURL returns HTTP 204, making it a cheap reachability check.
const connectivityProbeURL = "https://www.gstatic.com/generate_204"

// probeConnectivity reports whether the network currently reaches the
// internet. It is a best-effort check, not a guarantee.
func probeConnectivity() bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(connectivityProbeURL)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1))
	return resp.StatusCode == http.StatusNoContent
}
