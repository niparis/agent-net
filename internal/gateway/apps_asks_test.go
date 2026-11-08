package gateway

import (
	"context"
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
	"time"
)

func TestAppConsentAskClosesOnApproval(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)
	code, id := oauthPage(t, oauthAuthorize(h, client, nil))
	ask, err := s.OpenAppConsent(code, "bot", "Fixture")
	if err != nil {
		t.Fatal(err)
	}
	if ask.Kind != protocol.AskKindApproval || ask.Ref != "app-consent:"+id {
		t.Fatalf("ask: %+v", ask)
	}
	again, err := s.OpenAppConsent(code, "bot", "Fixture")
	if err != nil || again.ID != ask.ID {
		t.Fatalf("not idempotent: %+v %v", again, err)
	}
	if err := s.ApprovePending(id, "bot", "Fixture", c.t); err != nil {
		t.Fatal(err)
	}
	asks, err := s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range asks {
		if a.ID == ask.ID {
			t.Fatalf("completed consent stayed open: %+v", a)
		}
	}
}

func TestAppConsentAskClosesOnRefusalAndExpiry(t *testing.T) {
	for _, finish := range []string{"refuse", "expire"} {
		t.Run(finish, func(t *testing.T) {
			s, c, h := oauthFixture(t)
			client := oauthRegister(t, h)
			code, id := oauthPage(t, oauthAuthorize(h, client, nil))
			ask, err := s.OpenAppConsent(code, "bot", "Fixture")
			if err != nil {
				t.Fatal(err)
			}
			if finish == "refuse" {
				err = s.RefusePending(id)
			} else {
				c.t = c.t.Add(11 * time.Minute)
				err = s.reconcileAsks()
			}
			if err != nil {
				t.Fatal(err)
			}
			asks, err := s.ListAsks()
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range asks {
				if a.ID == ask.ID {
					t.Fatalf("consent stayed open: %+v", a)
				}
			}
		})
	}
}

func TestAppConsentRequiresOwnerAnswer(t *testing.T) {
	s, _, h := oauthFixture(t)
	client := oauthRegister(t, h)
	code, _ := oauthPage(t, oauthAuthorize(h, client, nil))
	ask, err := s.OpenAppConsent(code, "bot", "Fixture")
	if err != nil {
		t.Fatal(err)
	}
	handled, err := s.answerAppConsent(ask.ID, "approve", "", true)
	if !handled || err == nil {
		t.Fatalf("CTO consent: %v %v", handled, err)
	}
	handled, err = s.answerAppConsent(ask.ID, "approve", "", false)
	if !handled || err != nil {
		t.Fatalf("owner consent: %v %v", handled, err)
	}
	apps, err := s.ListApps()
	if err != nil || len(apps) != 1 {
		t.Fatalf("approval: %+v %v", apps, err)
	}
}

func TestPublicConsentAskAutoCloses(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir(), AppsListen: "127.0.0.1:0", FunnelRunner: &publicRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if err := g.openPublicConsent("https://login.tailscale.com/a/fixture", "Log in to Tailscale"); err != nil {
		t.Fatal(err)
	}
	asks, err := g.store.ListAsks()
	if err != nil || len(asks) != 1 {
		t.Fatalf("asks: %+v %v", asks, err)
	}
	if _, err := g.publicOperation(context.Background(), "apps.public.status", nil); err != nil {
		t.Fatal(err)
	}
	asks, err = g.store.ListAsks()
	if err != nil || len(asks) != 0 || !g.public.On {
		t.Fatalf("login completed: %+v on=%v %v", asks, g.public.On, err)
	}
}

func TestPhoneConsentAskOneClick(t *testing.T) {
	s, _, h, _ := phoneFixture(t)
	page := phoneRequest(h, "GET", "/", nil, "")
	codes := phoneCodeRE.FindStringSubmatch(page.Body.String())
	if len(codes) != 2 {
		t.Fatal("phone code missing")
	}
	ask, err := s.OpenAppConsent(codes[1], "", "My phone")
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := s.answerAppConsent(ask.ID, "approve", "", false); !handled || err != nil {
		t.Fatalf("phone approval: %v %v", handled, err)
	}
	asks, err := s.ListAsks()
	if err != nil || len(asks) != 0 {
		t.Fatalf("phone ask stayed open: %+v %v", asks, err)
	}
	apps, err := s.ListApps()
	if err != nil || len(apps) != 1 || apps[0].Kind != "owner_app" {
		t.Fatalf("phone app: %+v %v", apps, err)
	}
}

func TestAppConsentChangesReplaceDecision(t *testing.T) {
	s, _, h := oauthFixture(t)
	client := oauthRegister(t, h)
	code, _ := oauthPage(t, oauthAuthorize(h, client, nil))
	first, err := s.OpenAppConsent(code, "bot", "Fixture")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.OpenAppConsent(code, "owner_app", "Fixture")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("changed permissions kept the old consent ID")
	}
	if handled, err := s.answerAppConsent(first.ID, "approve", "", false); !handled || err == nil {
		t.Fatalf("old decision approved new permissions: %v %v", handled, err)
	}
	apps, err := s.ListApps()
	if err != nil || len(apps) != 0 {
		t.Fatalf("stale click created app: %+v %v", apps, err)
	}
}
