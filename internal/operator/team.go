package operator

import (
	"context"

	"github.com/niparis/agent-net/internal/protocol"
)

// WriteTeam changes one Team through the owner-only team_write method. Teams
// grant no authority: this only changes the label a Team gives to its members.
func (s *Client) WriteTeam(ctx context.Context, q protocol.TeamWrite) error {
	_, err := s.action(ctx, protocol.OpTeamWrite, q)
	return err
}
