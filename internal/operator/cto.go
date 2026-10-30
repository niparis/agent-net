package operator

import (
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
)

func (s *Client) CTOSend(ctx context.Context, text string) error {
	_, err := s.action(ctx, protocol.OpCTOSend, protocol.CTOSendRequest{Text: text})
	return err
}
func (s *Client) CTONew(ctx context.Context) error {
	_, err := s.action(ctx, protocol.OpCTONew, nil)
	return err
}
func (s *Client) CTOSettings(ctx context.Context) (protocol.CTOSettings, error) {
	var out protocol.CTOSettings
	b, err := s.action(ctx, protocol.OpCTOSettings, nil)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}
func (s *Client) CTOEvents(ctx context.Context, index int) (protocol.CTOEvents, error) {
	var out protocol.CTOEvents
	b, err := s.action(ctx, protocol.OpCTOEvents, protocol.CTOEventsRequest{Index: index})
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}
