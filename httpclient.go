// Copyright (c) 2013-2015 The btcsuite developers
// Copyright (c) 2015-2020 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"

	"github.com/decred/go-socks/socks"
	"github.com/jrick/wsrpc/v2"
)

// dialClient dials and returns a websocket JSON-RPC client that is configured
// according to the proxy TLS, and authentication settings in the application
// config.
func dialClient(ctx context.Context, cfg *config) (*wsrpc.Client, error) {
	var opts []wsrpc.Option

	// Configure proxy if needed.
	if cfg.Proxy != "" {
		proxy := &socks.Proxy{
			Addr:     cfg.Proxy,
			Username: cfg.ProxyUser,
			Password: cfg.ProxyPass,
		}
		dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := proxy.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return c, nil
		}
		opts = append(opts, wsrpc.WithDial(dial))
	}

	// Configure TLS if needed.
	if cfg.RPCCert != "" {
		tc := new(tls.Config)
		pem, err := ioutil.ReadFile(cfg.RPCCert)
		if err != nil {
			return nil, err
		}

		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM(pem); !ok {
			return nil, fmt.Errorf("invalid certificate file: %v",
				cfg.RPCCert)
		}
		tc.RootCAs = pool
		if cfg.AuthType == authTypeClientCert {
			keypair, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
			if err != nil {
				return nil, fmt.Errorf("read client keypair: %v", err)
			}
			tc.Certificates = []tls.Certificate{keypair}

		}
		opts = append(opts, wsrpc.WithTLSConfig(tc))
	}

	// Configure auth.
	user, pass := cfg.RPCUser, cfg.RPCPassword
	if user != "" || pass != "" {
		opts = append(opts, wsrpc.WithBasicAuth(user, pass))
	}

	return wsrpc.Dial(ctx, cfg.RPCServer, opts...)
}
