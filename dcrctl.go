// Copyright (c) 2013-2015 The btcsuite developers
// Copyright (c) 2015-2020 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"

	wallettypes "decred.org/dcrwallet/rpc/jsonrpc/types"
	"github.com/decred/dcrd/dcrjson/v3"
	dcrdtypes "github.com/decred/dcrd/rpc/jsonrpc/types/v2"
	"github.com/jrick/wsrpc/v2/agent"
)

const (
	showHelpMessage = "Specify -h to show available options"
	listCmdMessage  = "Specify -l to list available commands"
)

// commandUsage display the usage for a specific command.
func commandUsage(method interface{}) {
	usage, err := dcrjson.MethodUsageText(method)
	if err != nil {
		// This should never happen since the method was already checked
		// before calling this function, but be safe.
		fmt.Fprintln(os.Stderr, "Failed to obtain command usage:", err)
		return
	}

	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintf(os.Stderr, "  %s\n", usage)
}

func main() {
	cfg, args, err := loadConfig()
	if err != nil {
		os.Exit(1)
	}

	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "missing command parameter")
		usage()
	}

	// Ensure the specified method identifies a valid registered command and
	// is one of the usable types.
	methodStr := args[0]
	var method interface{} = dcrdtypes.Method(methodStr)
	usageFlags, err := dcrjson.MethodUsageFlags(method)
	if err != nil {
		method = wallettypes.Method(methodStr)
		usageFlags, err = dcrjson.MethodUsageFlags(method)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unrecognized command %q\n", methodStr)
		fmt.Fprintln(os.Stderr, listCmdMessage)
		os.Exit(1)
	}
	if usageFlags&unusableFlags != 0 {
		fmt.Fprintf(os.Stderr, "The '%s' command is unusable\n", method)
		os.Exit(1)
	}

	// Convert remaining command line args to a slice of interface values
	// to be passed along as parameters to new command creation function.
	//
	// Since some commands, such as submitblock, can involve data which is
	// too large for the Operating System to allow as a normal command line
	// parameter, support using '-' as an argument to allow the argument
	// to be read from a stdin pipe.
	bio := bufio.NewReader(os.Stdin)
	params := make([]interface{}, 0, len(args[1:]))
	for _, arg := range args[1:] {
		if arg == "-" {
			param, err := bio.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				fmt.Fprintf(os.Stderr, "Failed to read data "+
					"from stdin: %v\n", err)
				os.Exit(1)
			}
			if errors.Is(err, io.EOF) && len(param) == 0 {
				fmt.Fprintln(os.Stderr, "Not enough lines "+
					"provided on stdin")
				os.Exit(1)
			}
			param = strings.TrimRight(param, "\r\n")
			params = append(params, param)
			continue
		}

		params = append(params, arg)
	}

	// The only way to use dcrjson's argument parsing features is to create
	// the concrete command type boxed in an interface{}, and then marshal
	// this to a complete JSON-RPC request object.  So we do this, and then
	// immediately unmarsal the parameters contained within so they can be
	// passed to a far saner Call method.

	// Attempt to create the appropriate command using the arguments
	// provided by the user.
	cmd, err := dcrjson.NewCmd(method, params...)
	if err != nil {
		// Show the error along with its error code when it's a
		// dcrjson.Error as it realistically will always be since the
		// NewCmd function is only supposed to return errors of that
		// type.
		var jerr dcrjson.Error
		if errors.As(err, &jerr) {
			fmt.Fprintf(os.Stderr, "%s command: %v (code: %s)\n",
				method, err, jerr.Code)
			commandUsage(method)
			os.Exit(1)
		}

		// The error is not a dcrjson.Error and this really should not
		// happen.  Nevertheless, fallback to just showing the error
		// if it should happen due to a bug in the package.
		fmt.Fprintf(os.Stderr, "%s command: %v\n", method, err)
		commandUsage(method)
		os.Exit(1)
	}

	// Marshal the command into a JSON-RPC byte slice in preparation for
	// sending it to the RPC server.
	marshalledJSON, err := dcrjson.MarshalCmd("1.0", 1, cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Now the parameters are actually available.
	var requestObject struct {
		Params []json.RawMessage `json:"params"`
	}
	err = json.Unmarshal(marshalledJSON, &requestObject)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	callParams := make([]interface{}, len(requestObject.Params))
	for i := range callParams {
		callParams[i] = requestObject.Params[i]
	}

	// Caller is the client to perform the call.  This is either a newly
	// dialed wsrpc.Client, or (later) a connection to the agent process.
	var caller interface {
		Call(ctx context.Context, method string, result interface{}, args ...interface{}) error
	}

	ctx := context.Background()
	var result json.RawMessage
	// proxy isn't supported by the agent
	if cfg.Proxy == "" && agent.EnvironmentSet() {
		ag := &agent.Client{
			Address: cfg.RPCServer,
			User:    cfg.RPCUser,
			Pass:    cfg.RPCPassword,
		}
		if cfg.RPCCert != "" {
			pem, err := ioutil.ReadFile(cfg.RPCCert)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			ag.RootCert = string(pem)
		}
		caller = ag
	} else {
		caller, err = dialClient(ctx, cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	err = caller.Call(ctx, methodStr, &result, callParams...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if len(result) == 0 {
		return
	}

	// Choose how to display the result based on its type.
	if result[0] == '"' {
		var str string
		if err := json.Unmarshal(result, &str); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to unmarshal result: %v",
				err)
			os.Exit(1)
		}
		fmt.Println(str)
		return
	}

	if result[0] == '{' || result[0] == '[' {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		err := enc.Encode(result)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to format result: %v",
				err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("%s\n", result)
}
