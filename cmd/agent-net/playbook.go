package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

// The playbook: the owner's notes about a model (decision 0022). The CTO will
// edit these later; for now the owner writes them from the command line.

func cmdPlaybook(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: agent-net playbook set <model> [--harness H] <text>   |   agent-net playbook get <model> [--harness H]")
	}
	switch args[0] {
	case "set":
		return cmdPlaybookSet(ctx, args[1:])
	case "get":
		return cmdPlaybookGet(ctx, args[1:])
	case "pin", "unpin":
		return cmdPlaybookPin(ctx, args[0] == "pin", args[1:])
	}
	return fmt.Errorf("unknown playbook command %q (use set, get, pin or unpin)", args[0])
}

func playbookSetRequest(args []string) (protocol.PlaybookSetRequest, error) {
	fs := flag.NewFlagSet("playbook set", flag.ExitOnError)
	harness := fs.String("harness", "", "only this harness (empty: every harness)")
	pin := fs.Bool("pin", false, "pin the note, so automatic learning never changes it")
	unpin := fs.Bool("unpin", false, "unpin the note")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return protocol.PlaybookSetRequest{}, err
	}
	if len(pos) == 0 {
		return protocol.PlaybookSetRequest{}, errors.New("usage: agent-net playbook set <model> [--harness H] [--pin|--unpin] <text>")
	}
	req := protocol.PlaybookSetRequest{Model: pos[0], Harness: *harness, Note: strings.Join(pos[1:], " ")}
	if *pin || *unpin {
		req.Pin = pin
	}
	return req, nil
}

func cmdPlaybookSet(ctx context.Context, args []string) error {
	req, err := playbookSetRequest(args)
	if err != nil {
		return err
	}
	model, harness, note, err := protocol.CleanPlaybook(req.Model, req.Harness, req.Note)
	if err != nil {
		return err
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := playbookCall(ctx, cl, protocol.OpPlaybookSet, protocol.PlaybookSetRequest{Model: model, Harness: harness, Note: note, Pin: req.Pin})
	if err != nil {
		return err
	}
	var out protocol.PlaybookGetResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if note == "" {
		fmt.Printf("Removed the playbook note for %s%s.\n", model, harnessSuffix(harness))
		return nil
	}
	fmt.Printf("Saved the playbook note for %s%s.\n", model, harnessSuffix(harness))
	printPlaybook(os.Stdout, out.Note)
	return nil
}

func cmdPlaybookGet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("playbook get", flag.ExitOnError)
	harness := fs.String("harness", "", "only this harness (empty: every harness)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: agent-net playbook get <model> [--harness H]")
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := playbookCall(ctx, cl, protocol.OpPlaybookGet, protocol.PlaybookGetRequest{Model: pos[0], Harness: *harness})
	if err != nil {
		return err
	}
	var out protocol.PlaybookGetResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if out.Note == nil {
		fmt.Printf("No playbook note for %s%s.\n", pos[0], harnessSuffix(*harness))
		return nil
	}
	printPlaybook(os.Stdout, out.Note)
	return nil
}

func printPlaybook(w io.Writer, n *protocol.PlaybookNote) {
	if n == nil {
		return
	}
	pin := ""
	if n.Pinned {
		pin = " (pinned)"
	}
	fmt.Fprintf(w, "%s%s%s\n%s\n", n.Subject, harnessSuffix(n.Harness), pin, n.Note)
}

// cmdPlaybookPin pins or unpins the note that is already stored for a model.
func cmdPlaybookPin(ctx context.Context, pinned bool, args []string) error {
	fs := flag.NewFlagSet("playbook pin", flag.ExitOnError)
	harness := fs.String("harness", "", "only this harness (empty: every harness)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: agent-net playbook pin|unpin <model> [--harness H]")
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	var got protocol.PlaybookGetResult
	raw, err := playbookCall(ctx, cl, protocol.OpPlaybookGet, protocol.PlaybookGetRequest{Model: pos[0], Harness: *harness})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return err
	}
	if got.Note == nil {
		return fmt.Errorf("no playbook note for %s%s", pos[0], harnessSuffix(*harness))
	}
	raw, err = playbookCall(ctx, cl, protocol.OpPlaybookSet, protocol.PlaybookSetRequest{
		Model: got.Note.Subject, Harness: got.Note.Harness, Note: got.Note.Note, Pin: &pinned})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return err
	}
	if pinned {
		fmt.Printf("Pinned the playbook note for %s%s.\n", got.Note.Subject, harnessSuffix(got.Note.Harness))
	} else {
		fmt.Printf("Unpinned the playbook note for %s%s.\n", got.Note.Subject, harnessSuffix(got.Note.Harness))
	}
	return nil
}

func harnessSuffix(h string) string {
	if h == "" {
		return ""
	}
	return " (" + h + ")"
}

// playbookCall reports an older daemon that does not know the playbook.
func playbookCall(ctx context.Context, cl *client.Client, method string, params any) (json.RawMessage, error) {
	raw, err := cl.Call(ctx, method, params)
	if err != nil && (strings.Contains(err.Error(), "not available to cli clients") || strings.Contains(err.Error(), "unknown method")) {
		return nil, errors.New("this machine's daemon is too old for playbook notes; run `agent-net upgrade`")
	}
	return raw, err
}
