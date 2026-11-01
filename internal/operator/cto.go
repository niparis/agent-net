package operator

import (
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
)

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

func (s *Client) CTOGraphHistory(ctx context.Context, after int64) ([]protocol.CTOGraphChange, error) {
	var out []protocol.CTOGraphChange
	b, err := s.action(ctx, protocol.OpCTOGraphHistory, map[string]int64{"after": after})
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}
func (s *Client) CTOUndo(ctx context.Context, id int64) error {
	_, err := s.action(ctx, protocol.OpGraphUndo, protocol.GraphUndoRequest{ID: id, CTOChat: true})
	return err
}

func (s *Client) SetCTOSettings(ctx context.Context, req protocol.CTOSettingsRequest) (protocol.CTOSettings, error) {
	var out protocol.CTOSettings
	b, err := s.action(ctx, protocol.OpCTOSettings, req)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}

func (s *Client) CTOGraphWindow(ctx context.Context, toggle bool) (protocol.CTOGraphWindow, error) {
	var out protocol.CTOGraphWindow
	b, err := s.action(ctx, protocol.OpCTOGraphWindow, map[string]bool{"toggle": toggle})
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}
