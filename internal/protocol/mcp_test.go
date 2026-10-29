package protocol

import (
	"strings"
	"testing"
)

func TestValidMCPName(t *testing.T) {
	for _, ok := range []string{"a", "homelab", "my-server-2", "x1"} {
		if err := ValidMCPName(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Home", "my_server", "-a", "a-", "a--b", "a b", "a__b", "a.b", strings.Repeat("a", MCPMaxNameLen+1)} {
		if ValidMCPName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestMCPServerValidate(t *testing.T) {
	good := []MCPServer{
		{Name: "a", Transport: MCPStdio, Command: "x", Args: []string{"--flag"}, Env: []string{"HOME_TOKEN"}},
		{Name: "a", Transport: MCPHTTP, URL: "https://h/mcp", Headers: []MCPHeader{{Name: "Authorization", Env: "T"}}},
		{Name: "a", Transport: MCPSSE, URL: "http://h/sse", Headers: []MCPHeader{{Name: "X-Key", File: "~/.secrets/k"}}, TimeoutSec: 600},
	}
	for i, s := range good {
		if err := s.Validate(); err != nil {
			t.Errorf("good[%d]: %v", i, err)
		}
	}
	bad := map[string]MCPServer{
		"no command":          {Name: "a", Transport: MCPStdio},
		"stdio with url":      {Name: "a", Transport: MCPStdio, Command: "x", URL: "http://h"},
		"env with value":      {Name: "a", Transport: MCPStdio, Command: "x", Env: []string{"T=secret"}},
		"env with space":      {Name: "a", Transport: MCPStdio, Command: "x", Env: []string{"T X"}},
		"no url":              {Name: "a", Transport: MCPHTTP},
		"bad scheme":          {Name: "a", Transport: MCPHTTP, URL: "ftp://h"},
		"credentials in url":  {Name: "a", Transport: MCPHTTP, URL: "https://u:p@h/"},
		"url with command":    {Name: "a", Transport: MCPHTTP, URL: "http://h", Command: "x"},
		"header both sources": {Name: "a", Transport: MCPHTTP, URL: "http://h", Headers: []MCPHeader{{Name: "A", Env: "T", File: "/f"}}},
		"header no source":    {Name: "a", Transport: MCPHTTP, URL: "http://h", Headers: []MCPHeader{{Name: "A"}}},
		"header value as env": {Name: "a", Transport: MCPHTTP, URL: "http://h", Headers: []MCPHeader{{Name: "A", Env: "Bearer abc"}}},
		"bad header name":     {Name: "a", Transport: MCPHTTP, URL: "http://h", Headers: []MCPHeader{{Name: "A B", Env: "T"}}},
		"unknown transport":   {Name: "a", Transport: "ws", URL: "http://h"},
		"timeout too long":    {Name: "a", Transport: MCPStdio, Command: "x", TimeoutSec: 601},
		"negative timeout":    {Name: "a", Transport: MCPStdio, Command: "x", TimeoutSec: -1},
		"bad name":            {Name: "A", Transport: MCPStdio, Command: "x"},
	}
	for what, s := range bad {
		err := s.Validate()
		if err == nil {
			t.Errorf("%s accepted", what)
			continue
		}
		for _, leak := range []string{"secret", "Bearer abc", "u:p"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: error echoes %q: %v", what, leak, err)
			}
		}
	}
}

func TestMCPTimeoutDefault(t *testing.T) {
	if (MCPServer{}).Timeout() != MCPDefaultTimeout || (MCPServer{TimeoutSec: 5}).Timeout().Seconds() != 5 {
		t.Fatal("timeout")
	}
}
