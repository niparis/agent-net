package gateway

import "testing"

func TestInboxFromOutcome(t *testing.T) {
	s, _ := newTestStore(t)
	item, err := s.inboxFromOutcome("rejection", "s_1", "p", "reason", "recommendation", "m_1", "c_1")
	if err != nil || item.Kind != "rejection" || item.SessionID != "s_1" || item.ConversationID != "c_1" {
		t.Fatalf("item: %+v, %v", item, err)
	}
	if _, err := s.inboxFromOutcome("accept", "s_1", "p", "reason", "", "m_1", "c_1"); err == nil {
		t.Fatal("accepted outcome created inbox item")
	}
}

func TestInboxFromOutcomeUsesOutcomeNames(t *testing.T) {
	s, _ := newTestStore(t)
	for outcome, kind := range map[string]string{"reject": "rejection", "escalate": "escalation"} {
		item, err := s.inboxFromOutcome(outcome, "s_1", "project", "reason", "", "m_1", "c_1")
		if err != nil || item.Kind != kind {
			t.Fatalf("%s: %+v %v", outcome, item, err)
		}
	}
}
