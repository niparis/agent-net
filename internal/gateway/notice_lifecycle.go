package gateway

import "fmt"

// retireObsoleteNotices finalizes checks that no longer represent the current
// question, and step notices with no remaining current task association. A
// check for a session that waits on its owner never represents it. A batch
// with any current association is kept for delivery.
func (s *Store) retireObsoleteNotices() error {
	_, err := s.db.Exec(fmt.Sprintf(`UPDATE messages SET state='expired'
 WHERE sender='agent-net' AND state IN ('stored','dispatched') AND (
 (idem_key LIKE 'check:%%' AND (NOT EXISTS (
   SELECT 1 FROM session_checks c WHERE c.session_id=messages.recipient AND c.check_waiting=1
   AND messages.idem_key='check:' || c.session_id || ':' || c.check_day || ':' || c.check_day_count)
   OR `+ownerBlockedSQL+`))
 OR (idem_key GLOB 'step_*' AND NOT EXISTS (
   SELECT 1 FROM delivery_step_messages d
   JOIN delivery_step_tasks t ON t.change_id=d.change_id AND t.revision=d.revision AND t.step=d.step
    AND t.for_step=d.for_step AND t.after_merge=d.after_merge
   JOIN delivery_changes c ON c.id=t.change_id
   WHERE d.message_id=messages.id AND t.revision=c.revision AND t.completed=0
    AND (t.claimed_by='' OR t.claimed_by=d.session_id)
    AND ((t.after_merge=0 AND c.state='open') OR (t.after_merge=1 AND c.state='merged')))))`, "messages.recipient"))
	return err
}
