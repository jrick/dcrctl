// Copyright (c) 2013-2015 The btcsuite developers
// Copyright (c) 2015-2020 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/decred/dcrd/dcrjson/v3"
	"github.com/decred/dcrd/dcrutil/v3"
	"github.com/jrick/flagfile"

	wallettypes "decred.org/dcrwallet/rpc/jsonrpc/types"
	dcrdtypes "github.com/decred/dcrd/rpc/jsonrpc/types/v2"
)

const (
	// unusableFlags are the command usage flags which this utility are not
	// able to use.  In particular it doesn't support websockets and
	// consequently notifications.
	unusableFlags = dcrjson.UFWebsocketOnly | dcrjson.UFNotification
)

// Authorization types.
const (
	authTypeBasic      = "basic"
	authTypeClientCert = "clientcert"
)

var (
	dcrdHomeDir            = dcrutil.AppDataDir("dcrd", false)
	dcrctlHomeDir          = dcrutil.AppDataDir("dcrctl", false)
	dcrwalletHomeDir       = dcrutil.AppDataDir("dcrwallet", false)
	defaultConfigFile      = filepath.Join(dcrctlHomeDir, "dcrctl.conf")
	defaultClientCertFile  = filepath.Join(dcrctlHomeDir, "client.pem")
	defaultClientKeyFile   = filepath.Join(dcrctlHomeDir, "client-key.pem")
	defaultRPCServer       = "localhost"
	defaultWalletRPCServer = "localhost"
	defaultRPCCertFile     = filepath.Join(dcrdHomeDir, "rpc.cert")
	defaultWalletCertFile  = filepath.Join(dcrwalletHomeDir, "rpc.cert")
)

// listCommands categorizes and lists all of the usable commands along with
// their one-line usage.
func listCommands() {
	var categories = []struct {
		Header string
		Method interface{}
		Usages []string
	}{{
		Header: "Chain Server Commands:",
		Method: dcrdtypes.Method(""),
	}, {
		Header: "Wallet Server Commands (--wallet):",
		Method: wallettypes.Method(""),
	}}

	for i := range categories {
		method := categories[i].Method
		methods := dcrjson.RegisteredMethods(method)
		for _, methodStr := range methods {
			switch method.(type) {
			case dcrdtypes.Method:
				method = dcrdtypes.Method(methodStr)
			case wallettypes.Method:
				method = wallettypes.Method(methodStr)
			}

			flags, err := dcrjson.MethodUsageFlags(method)
			if err != nil {
				// This should never happen since the method was just
				// returned from the package, but be safe.
				continue
			}

			// Skip the commands that aren't usable from this utility.
			if flags&unusableFlags != 0 {
				continue
			}

			usage, err := dcrjson.MethodUsageText(method)
			if err != nil {
				// This should never happen since the method was just
				// returned from the package, but be safe.
				continue
			}

			categories[i].Usages = append(categories[i].Usages, usage)
		}
	}

	// Display the command according to their categories.
	for i := range categories {
		fmt.Println(categories[i].Header)
		for _, usage := range categories[i].Usages {
			fmt.Println(usage)
		}
		fmt.Println()
	}
}

// config defines the configuration options for dcrctl.
//
// See loadConfig for details on the configuration load process.
type config struct {
	Config       flag.Value
	ShowVersion  bool
	ListCommands bool
	RPCServer    string
	Wallet       bool
	TestNet      bool
	SimNet       bool
	RPCUser      string
	RPCPassword  string
	RPCCert      string
	Proxy        string
	ProxyUser    string
	ProxyPass    string
	AuthType     string
	ClientCert   string
	ClientKey    string
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage of dcrctl:
  dcrctl [flags] command <args...>

Flags:
  -C value
        config file
  -V    show version and exit
  -l    list commands and exit
  -s string
        Websocket server URL (default "wss://localhost/ws")
  -wallet
        default to dcrwallet ports
  -simnet
        default to simnet ports
  -testnet
        default to testnet ports
  -authtype string (default "basic")
        authentication method (one of: "basic" "clientcert")
  -u/-rpcuser string
        RPC user
  -P/-rpcpass string
        RPC password
  -clientcert string
        certificate file for clientcert authentication
  -clientkey string
        key file for clientcert authentication
  -c/-rpccert string
        filepath to Certificate Authority; uses global cert store when empty
  -proxy string
        SOCKS5 proxy
  -proxypass string
        SOCKS5 proxy password
  -proxyuser string
        SOCKS5 proxy username
`)
	os.Exit(2)
}

func (c *config) FlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("dcrctl", flag.ExitOnError)
	configParser := flagfile.Parser{AllowUnknown: true}
	c.Config = configParser.ConfigFlag(fs)
	fs.Var(c.Config, "C", "config file")
	fs.BoolVar(&c.ShowVersion, "V", false, "")
	fs.BoolVar(&c.ListCommands, "l", false, "")
	fs.StringVar(&c.RPCServer, "s", "wss://localhost/ws", "")
	fs.BoolVar(&c.TestNet, "testnet", false, "")
	fs.BoolVar(&c.SimNet, "simnet", false, "")
	fs.BoolVar(&c.Wallet, "wallet", false, "")
	fs.StringVar(&c.RPCUser, "u", "", "")
	fs.StringVar(&c.RPCUser, "rpcuser", "", "")
	fs.StringVar(&c.RPCPassword, "P", "", "")
	fs.StringVar(&c.RPCPassword, "rpcpass", "", "")
	fs.StringVar(&c.RPCCert, "c", "", "")
	fs.StringVar(&c.RPCCert, "rpccert", "", "")
	fs.StringVar(&c.Proxy, "proxy", "", "")
	fs.StringVar(&c.ProxyUser, "proxyuser", "", "")
	fs.StringVar(&c.ProxyPass, "proxypass", "", "")
	fs.StringVar(&c.AuthType, "authtype", "", "")
	fs.StringVar(&c.ClientCert, "clientcert", "", "")
	fs.StringVar(&c.ClientKey, "clientkey", "", "")
	fs.Usage = usage
	return fs
}

// cleanAndExpandPath expands environment variables and leading ~ in the
// passed path, cleans the result, and returns it.
func cleanAndExpandPath(path string) string {
	// Nothing to do when no path is given.
	if path == "" {
		return path
	}

	// NOTE: The os.ExpandEnv doesn't work with Windows cmd.exe-style
	// %VARIABLE%, but the variables can still be expanded via POSIX-style
	// $VARIABLE.
	path = os.ExpandEnv(path)

	if !strings.HasPrefix(path, "~") {
		return filepath.Clean(path)
	}

	// Expand initial ~ to the current user's home directory, or ~otheruser
	// to otheruser's home directory.  On Windows, both forward and backward
	// slashes can be used.
	path = path[1:]

	var pathSeparators string
	if runtime.GOOS == "windows" {
		pathSeparators = string(os.PathSeparator) + "/"
	} else {
		pathSeparators = string(os.PathSeparator)
	}

	userName := ""
	if i := strings.IndexAny(path, pathSeparators); i != -1 {
		userName = path[:i]
		path = path[i:]
	}

	homeDir := ""
	var u *user.User
	var err error
	if userName == "" {
		u, err = user.Current()
	} else {
		u, err = user.Lookup(userName)
	}
	if err == nil {
		homeDir = u.HomeDir
	}
	// Fallback to CWD if user lookup fails or user has no home directory.
	if homeDir == "" {
		homeDir = "."
	}

	return filepath.Join(homeDir, path)
}

// fileExists reports whether the named file or directory exists.
func fileExists(name string) bool {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// loadConfig initializes and parses the config using a config file and command
// line options.
//
// The configuration proceeds as follows:
// 	1) Start with a default config with sane settings
// 	2) Pre-parse the command line to check for an alternative config file
// 	3) Load configuration file overwriting defaults with any specified options
// 	4) Parse CLI options and overwrite/add any specified options
//
// The above results in functioning properly without any config settings
// while still allowing the user to override settings with config files and
// command line options.  Command line options always take precedence.
func loadConfig() (*config, []string, error) {
	// Default config.
	cfg := &config{
		RPCServer: defaultRPCServer,
	}
	fs := cfg.FlagSet()
	args := os.Args[1:]

	// Determine config file to read (if any).  When -C is the first
	// parameter, configure flags from the specified config file rather than
	// using the application default path.  Otherwise the default config
	// will be parsed if the file exists.
	//
	// If further -C options are specified in later arguments, the config
	// file parameter is used to modify the current state of the config.
	//
	// If you want to read the application default config first, and other
	// configs later, explicitly specify the default path with the first
	// flag argument.
	var configPath string
	if len(args) >= 2 && args[0] == "-C" {
		configPath = args[1]
		args = args[2:]
	} else if fileExists(defaultConfigFile) {
		configPath = defaultConfigFile
	}
	if configPath != "" {
		err := cfg.Config.Set(configPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fs.Parse(args)

	// Show the version and exit if the version flag was specified.
	appName := filepath.Base(os.Args[0])
	appName = strings.TrimSuffix(appName, filepath.Ext(appName))
	if cfg.ShowVersion {
		fmt.Printf("%s version %s (Go version %s %s/%s)\n", appName,
			versionString(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		os.Exit(0)
	}

	// Show the available commands and exit if the associated flag was
	// specified.
	if cfg.ListCommands {
		listCommands()
		os.Exit(0)
	}

	// Multiple networks can't be selected simultaneously.
	numNets := 0
	if cfg.TestNet {
		numNets++
	}
	if cfg.SimNet {
		numNets++
	}
	if numNets > 1 {
		str := "%s: the testnet and simnet params can't be used " +
			"together -- choose one of the two"
		err := fmt.Errorf(str, "loadConfig")
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, err
	}

	// Override the RPC certificate if the --wallet flag was specified and
	// the user did not specify one.
	switch {
	case cfg.Wallet && cfg.RPCCert == "" && fileExists(defaultWalletCertFile):
		cfg.RPCCert = defaultWalletCertFile
	case cfg.RPCCert == "" && fileExists(defaultRPCCertFile):
		cfg.RPCCert = defaultRPCCertFile
	}

	// Set path for the client key/cert for the clientcert authorization type
	// when they're specified.
	if cfg.AuthType == authTypeClientCert {
		if cfg.ClientCert == "" {
			cfg.ClientCert = defaultClientCertFile
		}
		if cfg.ClientKey == "" {
			cfg.ClientKey = defaultClientKeyFile
		}

		cfg.ClientCert = cleanAndExpandPath(cfg.ClientCert)
		cfg.ClientKey = cleanAndExpandPath(cfg.ClientKey)
	}

	// Handle environment variable expansion in the RPC certificate path.
	cfg.RPCCert = cleanAndExpandPath(cfg.RPCCert)

	// Add default port to RPC server based on --testnet and --wallet flags
	// if needed.
	server, err := normalizeServer(cfg)
	if err != nil {
		println(err.Error())
		return nil, nil, err
	}
	cfg.RPCServer = server

	return cfg, fs.Args(), nil
}

func normalizeServer(cfg *config) (string, error) {
	s := cfg.RPCServer
	parsed, err := url.Parse(s)
	if err != nil {
		return "", err
	}
	_, _, err = net.SplitHostPort(parsed.Host)
	if err != nil {
		port := defaultPort(cfg)
		parsed.Host = net.JoinHostPort(parsed.Host, port)
	}
	return parsed.String(), nil
}

func defaultPort(cfg *config) string {
	port := "9109"
	switch {
	case cfg.Wallet && cfg.TestNet:
		port = "19110"
	case cfg.Wallet && cfg.SimNet:
		port = "19557"
	case cfg.TestNet:
		port = "19109"
	case cfg.SimNet:
		port = "19556"
	}
	return port
}
