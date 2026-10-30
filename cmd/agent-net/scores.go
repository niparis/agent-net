package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

func scoreArguments(args []string) (string, *protocol.ScoreSetRequest, error) {
	usage := errors.New("usage: agent-net scores [set <model> <score> | clear <model>]")
	if len(args) == 0 {
		return protocol.OpScoresGet, nil, nil
	}
	if len(args) == 2 && args[0] == "clear" && strings.TrimSpace(args[1]) != "" {
		return protocol.OpScoresSet, &protocol.ScoreSetRequest{Model: args[1]}, nil
	}
	if len(args) == 3 && args[0] == "set" && strings.TrimSpace(args[1]) != "" {
		score, err := strconv.ParseFloat(args[2], 64)
		if err == nil && !math.IsNaN(score) && score >= 0 && score <= 100 {
			return protocol.OpScoresSet, &protocol.ScoreSetRequest{Model: args[1], Score: &score}, nil
		}
	}
	return "", nil, usage
}
func cmdScores(ctx context.Context, args []string) error {
	method, request, err := scoreArguments(args)
	if err != nil {
		return err
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := cl.Call(ctx, method, request)
	if err != nil {
		return err
	}
	var scores []protocol.ModelScore
	if err = json.Unmarshal(raw, &scores); err != nil {
		return err
	}
	fmt.Println("Owner overrides:")
	for _, s := range scores {
		fmt.Printf("%s  %g\n", s.Match, s.Score)
	}
	fmt.Println("Embedded scores:")
	for _, s := range protocol.EmbeddedModelScores() {
		fmt.Printf("%s  %g (%s)\n", s.Match, s.Score, s.Source)
	}
	return nil
}
