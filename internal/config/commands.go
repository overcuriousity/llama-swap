package config

import (
	"fmt"
	"net"
	"runtime"
	"strings"

	"github.com/billziss-gh/golib/shlex"
)

func SanitizeCommand(cmdStr string) ([]string, error) {
	var cleanedLines []string
	for _, line := range strings.Split(cmdStr, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip comment lines
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Handle trailing backslashes by replacing with space
		if strings.HasSuffix(trimmed, "\\") {
			cleanedLines = append(cleanedLines, strings.TrimSuffix(trimmed, "\\")+" ")
		} else {
			cleanedLines = append(cleanedLines, line)
		}
	}

	// put it back together
	cmdStr = strings.Join(cleanedLines, "\n")

	// Split the command into arguments
	var args []string
	if runtime.GOOS == "windows" {
		args = shlex.Windows.Split(cmdStr)
	} else {
		args = shlex.Posix.Split(cmdStr)
	}

	// Ensure the command is not empty
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	return args, nil
}

// ParseRPCEndpoints extracts RPC endpoints from command string
// Handles: --rpc host:port,host2:port2 or --rpc=host:port or -rpc host:port
func ParseRPCEndpoints(cmdStr string) ([]string, error) {
	args, err := SanitizeCommand(cmdStr)
	if err != nil {
		return nil, err
	}

	var endpoints []string
	for i, arg := range args {
		if arg == "--rpc" || arg == "-rpc" {
			// Collect all non-flag arguments after --rpc
			// This handles Windows where shlex splits single-quoted strings with spaces
			var parts []string
			for j := i + 1; j < len(args) && !strings.HasPrefix(args[j], "-"); j++ {
				parts = append(parts, args[j])
			}
			if len(parts) > 0 {
				// Join parts with space and parse as a single endpoint list
				endpoints = parseEndpointList(strings.Join(parts, " "))
			}
		} else if strings.HasPrefix(arg, "--rpc=") {
			endpoints = parseEndpointList(strings.TrimPrefix(arg, "--rpc="))
		} else if strings.HasPrefix(arg, "-rpc=") {
			endpoints = parseEndpointList(strings.TrimPrefix(arg, "-rpc="))
		}
	}

	// Validate each endpoint
	for _, ep := range endpoints {
		if _, _, err := net.SplitHostPort(ep); err != nil {
			return nil, fmt.Errorf("invalid RPC endpoint %q: %w", ep, err)
		}
	}

	return endpoints, nil
}

func parseEndpointList(s string) []string {
	s = strings.TrimSpace(s)

	// Strip surrounding quotes (both single and double) from the whole string
	// if they match. This handles cases like: "host:port,host2:port2"
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			s = s[1 : len(s)-1]
		}
	}

	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// Strip any remaining leading/trailing quotes from individual parts
		// This handles Windows where shlex doesn't handle single quotes and
		// may split 'host:port, host2:port' into "'host:port," and "host2:port'"
		p = strings.Trim(p, "'\"")
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func StripComments(cmdStr string) string {
	var cleanedLines []string
	for _, line := range strings.Split(cmdStr, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip comment lines
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		cleanedLines = append(cleanedLines, line)
	}
	return strings.Join(cleanedLines, "\n")
}
