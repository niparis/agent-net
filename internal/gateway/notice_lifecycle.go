package gateway

import (
	"fmt"
)

// retireObsoleteNotices finalizes checks that no longer represent the current
// question, and step notices with no remaining current task association. A
// check for a session that waits on its owner never represents it. A batch
// with any current association is kept for delivery.
func (s *Store) retireObsoleteNotices() error {
	// A task whose phase ended must be closed before its notices are judged,
	// or a claimed task keeps a notice alive forever.
	if err := s.retireObsoleteStepTasks(); err != nil {
		return err
	}
	res, err := s.db.Exec(fmt.Sprintf(`UPDATE messages SET state='expired'
 WHERE sender='agent-net' AND state IN ('stored','dispatched') AND
 (idem_key LIKE 'check:%%' AND (NOT EXISTS (
   SELECT 1 FROM sessions c WHERE c.id=messages.recipient AND c.check_waiting=1
   AND messages.idem_key='check:' || c.id || ':' || c.check_day || ':' || c.check_day_count)
   OR `+ownerBlockedSQL+`))`, "messages.recipient"))
	if err != nil {
		return err
	}
	expired, _ := res.RowsAffected()
	n, err := retireUnlinkedStepMessages(s.db)
	if err != nil {
		return err
	}
	expired += n
	if n, err = retireEmptyNudgeBatches(s.db); err != nil {
		return err
	}
	expired += n
	if expired > 0 {
		s.logf("notices.retired", "count", expired)
	}
	return nil
}
