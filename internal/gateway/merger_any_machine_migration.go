package gateway

// mergerAnyMachineMigration recreates the merger_replaced trigger of the step
// tables rework without its filter on the change's machine. A project has one
// merger, and a change made on any machine is pushed to origin, so replacing
// the merger session moves the open merger work of every change in the repo.
const mergerAnyMachineMigration = `
-- merger work of changes from every machine follows a replaced merger
DROP TRIGGER IF EXISTS merger_replaced;
CREATE TRIGGER merger_replaced AFTER UPDATE OF merger_session ON repos
WHEN OLD.merger_session<>'' AND NEW.merger_session<>'' AND OLD.merger_session<>NEW.merger_session BEGIN
UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched','failed') AND id IN (
SELECT d.value FROM delivery_step_tasks t JOIN delivery_changes c ON c.id=t.change_id JOIN json_each(t.notices) d
WHERE t.owner_node='/merger' AND c.project=OLD.id);
UPDATE delivery_step_tasks SET notices='{}',owner_node='/merger',claimed_by='',urgent=1
WHERE owner_node='/merger' AND change_id IN (SELECT c.id FROM delivery_changes c WHERE c.project=OLD.id);
END;
`
