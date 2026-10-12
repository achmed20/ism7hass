package ism7

import (
	"context"
	"crypto/tls"
	_ "embed"
	"fmt"
	"net"
	"strconv"
	"time"
)

// client.pem is the public client certificate/key shipped with Wolf's Smartset application
// (signed by "LuCon Root CA (direct)"); the ISM7 refuses clients without it.
//
//go:embed resources/client.pem
var clientPEM []byte

// Dial opens the TLS connection to the ISM7. The device uses a self signed chain and legacy
// cipher suites, so verification is skipped and RSA key exchange suites are enabled.
func Dial(ctx context.Context, host string, port uint16) (net.Conn, error) {
	cert, err := tls.X509KeyPair(clientPEM, clientPEM)
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}
	d := net.Dialer{Timeout: 30 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, &tls.Config{
		ServerName:         "ism7.server",
		InsecureSkipVerify: true, // device certificate is not trusted by any public CA
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         tls.VersionTLS12,
		// ponytail: Go has no TLS_RSA_WITH_AES_256_CBC_SHA256 (suite 0x3d); if the device insists on it,
		// a patched crypto/tls is needed.
		CipherSuites: []uint16{
			tls.TLS_RSA_WITH_RC4_128_SHA,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		},
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &cert, nil // always send ours, the CA list of the device is not matched by Go's logic
		},
	})
	hctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(hctx); err != nil {
		raw.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}
	return conn, nil
}
