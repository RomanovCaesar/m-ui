package app

import (
	"testing"
	"time"
)

func TestMonthlyResetDayFollowsShortMonths(t *testing.T) {
	at := func(year int, month time.Month, day, hour int) time.Time {
		return time.Date(year, month, day, hour, 0, 0, 0, time.UTC)
	}
	cases := []struct {
		day       int
		last, now time.Time
		due       bool
	}{
		{15, at(2026, 3, 15, 1), at(2026, 4, 14, 23), false},
		{15, at(2026, 3, 15, 1), at(2026, 4, 15, 0), true},
		{0, at(2026, 3, 31, 1), at(2026, 4, 1, 0), true}, // 0 means the 1st
		{31, at(2026, 1, 31, 1), at(2026, 2, 27, 23), false},
		{31, at(2026, 1, 31, 1), at(2026, 2, 28, 0), true}, // February has no 31st
		{31, at(2026, 2, 28, 1), at(2026, 3, 30, 23), false},
		{31, at(2026, 2, 28, 1), at(2026, 3, 31, 0), true},
		{20, at(2025, 12, 20, 9), at(2026, 1, 5, 9), false},
		{20, at(2025, 11, 20, 9), at(2026, 1, 5, 9), true}, // a month missed while the panel was down
	}
	for _, item := range cases {
		if got := trafficResetDue("monthly", item.day, item.last, item.now); got != item.due {
			t.Errorf("day %d, last %s, now %s: due = %v", item.day, item.last.Format(time.DateTime), item.now.Format(time.DateTime), got)
		}
	}
	if trafficResetDue("daily", 0, at(2026, 3, 5, 1), at(2026, 3, 4, 23)) {
		t.Error("a last reset in the future must not trigger a reset")
	}
}

func TestRenewClient(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	expired := func(at time.Time) Client {
		return Client{Enabled: true, ExpiryTime: at.UnixMilli(), Total: 100, Traffic: TrafficStats{Up: 60, Down: 40, AllTimeUp: 60, AllTimeDown: 40}}
	}
	cases := []struct {
		name   string
		client Client
		expiry time.Time
		count  int
	}{
		{"every 30 days", func() Client { c := expired(now.Add(-time.Hour)); c.RenewDays = 30; return c }(), now.Add(-time.Hour).AddDate(0, 0, 30), 1},
		{"catches up", func() Client { c := expired(now.AddDate(0, 0, -25)); c.RenewDays = 10; return c }(), now.AddDate(0, 0, -25).AddDate(0, 0, 30), 3},
		{"monthly on the 31st", func() Client {
			c := expired(time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC))
			c.RenewDay = 31
			return c
		}(), time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), 1},
		{"weekly on Monday", func() Client {
			c := expired(time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)) // a Monday
			c.RenewWeekday = 1
			return c
		}(), time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC), 1},
		{"weekly on Sunday", func() Client {
			c := expired(time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)) // a Tuesday
			c.RenewWeekday = 7
			return c
		}(), time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC), 1},
	}
	for _, item := range cases {
		client := item.client
		if !renewClient(&client, now) {
			t.Fatalf("%s: not renewed", item.name)
		}
		if client.ExpiryTime != item.expiry.UnixMilli() || client.RenewCount != item.count {
			t.Fatalf("%s: expiry %s count %d, want %s count %d", item.name, time.UnixMilli(client.ExpiryTime).UTC().Format(time.DateTime), client.RenewCount, item.expiry.Format(time.DateTime), item.count)
		}
		if client.Traffic.Up != 0 || client.Traffic.Down != 0 || client.Traffic.AllTimeUp != 60 || clientDepleted(client, now.UnixMilli()) {
			t.Fatalf("%s: renewal must clear the period's traffic only: %+v", item.name, client.Traffic)
		}
	}

	// The renewal limit stops it; a client that is not expired or has no renewal is left alone.
	limited := expired(now.Add(-time.Hour))
	limited.RenewDays, limited.RenewMax, limited.RenewCount = 30, 2, 2
	plain := expired(now.Add(-time.Hour))
	active := Client{Enabled: true, ExpiryTime: now.Add(time.Hour).UnixMilli(), RenewDays: 30}
	for _, client := range []*Client{&limited, &plain, &active} {
		before := *client
		if renewClient(client, now) || client.ExpiryTime != before.ExpiryTime || client.Traffic != before.Traffic {
			t.Fatalf("unexpected renewal: %+v", client)
		}
	}
	// The last allowed renewals are spent even when they do not reach today.
	short := expired(now.AddDate(0, 0, -25))
	short.RenewDays, short.RenewMax = 10, 2
	if !renewClient(&short, now) || short.RenewCount != 2 || short.Traffic.Up != 60 || !clientDepleted(short, now.UnixMilli()) {
		t.Fatalf("partial catch-up: %+v", short)
	}
}

func TestScheduledResetsPerClient(t *testing.T) {
	m := newStateTestManager(t.TempDir())
	m.state = defaultState()
	m.state.Settings.TimeLocation = "UTC"
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	used := TrafficStats{Up: 5, Down: 5, AllTimeUp: 5, AllTimeDown: 5}
	m.state.Inbounds = []Inbound{{
		ID: "in", Name: "in", Type: "vless", Enabled: true, TrafficReset: "never", Traffic: used, CreatedAt: yesterday,
		Clients: []Client{
			{ID: "daily", Name: "daily", Enabled: true, Total: 10, Traffic: used, TrafficReset: "daily", LastTrafficReset: yesterday},
			{ID: "off", Name: "off", Enabled: false, Traffic: used, TrafficReset: "daily", LastTrafficReset: yesterday},
			{ID: "none", Name: "none", Enabled: true, Traffic: used, CreatedAt: yesterday},
			{ID: "renew", Name: "renew", Enabled: true, Traffic: used, ExpiryTime: time.Now().Add(-time.Minute).UnixMilli(), RenewDays: 30},
		},
	}}
	m.enforcedClients["in:daily"] = true // over its quota, shut out of the configuration
	m.applyScheduledTrafficResets()

	clients := m.state.Inbounds[0].Clients
	if clients[0].Traffic.Up != 0 || clients[0].Traffic.AllTimeUp != 5 || clients[0].LastTrafficReset == yesterday {
		t.Fatalf("daily client must be reset: %+v", clients[0])
	}
	if clients[1].Traffic.Up != 5 || clients[2].Traffic.Up != 5 {
		t.Fatalf("a disabled client and a client without schedule keep their traffic: %+v %+v", clients[1], clients[2])
	}
	if clients[3].Traffic.Up != 0 || clients[3].RenewCount != 1 || clientDepleted(clients[3], time.Now().UnixMilli()) {
		t.Fatalf("expired client must be renewed: %+v", clients[3])
	}
	if m.state.Inbounds[0].Traffic.Up != 5 {
		t.Fatal("a client reset must not reset the inbound")
	}
	if m.enforcedClients["in:daily"] {
		t.Fatal("a client the reset brought back must leave the enforced set")
	}
}

func TestClientScheduleSurvivesSaves(t *testing.T) {
	m := newStateTestManager(t.TempDir())
	m.state = defaultState()
	old := time.Now().AddDate(0, -3, 0).Format(time.RFC3339)
	m.state.Inbounds = []Inbound{{ID: "in", Name: "in", Type: "vless", Enabled: true, TrafficReset: "never", Clients: []Client{
		{ID: "c", Name: "c", UUID: "11111111-1111-4111-8111-111111111111", Enabled: true, CreatedAt: old, FirstOnline: old,
			Traffic: TrafficStats{Up: 7}, RenewDays: 30, RenewCount: 4, TrafficReset: "monthly", LastTrafficReset: old},
	}}}

	// The form does not send the counters the panel owns.
	edited := Client{ID: "c", Name: "c", UUID: "11111111-1111-4111-8111-111111111111", Enabled: true, RenewDays: 30, TrafficReset: "monthly"}
	if err := m.saveClient("in", edited); err != nil {
		t.Fatal(err)
	}
	saved := m.state.Inbounds[0].Clients[0]
	if saved.RenewCount != 4 || saved.LastTrafficReset != old || saved.FirstOnline != old || saved.Traffic.Up != 7 {
		t.Fatalf("an unchanged schedule must keep its counters: %+v", saved)
	}

	// A new schedule starts now, so an old client is not reset at once.
	edited.TrafficReset = "daily"
	if err := m.saveClient("in", edited); err != nil {
		t.Fatal(err)
	}
	m.applyScheduledTrafficResets()
	if saved := m.state.Inbounds[0].Clients[0]; saved.Traffic.Up != 7 || saved.LastTrafficReset == old {
		t.Fatalf("changing the schedule must restart the period: %+v", saved)
	}

	for _, bad := range []Client{
		{TrafficReset: "yearly"},
		{TrafficReset: "monthly", TrafficResetDay: 32},
		{RenewDays: 30, RenewDay: 1},
		{RenewWeekday: 8},
		{RenewDays: -1},
	} {
		bad.ID, bad.Name, bad.UUID, bad.Enabled = "c", "c", "11111111-1111-4111-8111-111111111111", true
		if err := m.saveClient("in", bad); err == nil {
			t.Fatalf("invalid schedule accepted: %+v", bad)
		}
	}
	if err := validateInbound(Inbound{Type: "vless", TrafficReset: "monthly", TrafficResetDay: 40}); err == nil {
		t.Fatal("inbound reset day 40 accepted")
	}
}

func TestInboundLimitsAreEnforcedWhileRunning(t *testing.T) {
	m := newStateTestManager(t.TempDir())
	m.state = defaultState()
	m.state.Settings.TimeLocation = "UTC"
	lastMonth := time.Now().UTC().AddDate(0, -1, -1).Format(time.RFC3339)
	m.state.Inbounds = []Inbound{
		{ID: "quota", Name: "quota", Type: "vless", Enabled: true, Total: 10, Traffic: TrafficStats{Up: 4, Down: 6},
			TrafficReset: "monthly", LastTrafficReset: lastMonth, CreatedAt: lastMonth},
		{ID: "expired", Name: "expired", Type: "vless", Enabled: true, ExpiryTime: time.Now().Add(-time.Minute).UnixMilli()},
		{ID: "off", Name: "off", Type: "vless", Enabled: false},
		{ID: "fine", Name: "fine", Type: "vless", Enabled: true, Total: 10, Traffic: TrafficStats{Up: 1}},
	}
	m.enforceClientLimits()
	if !m.enforcedClients["quota"] || !m.enforcedClients["expired"] || m.enforcedClients["off"] || m.enforcedClients["fine"] {
		t.Fatalf("enforced inbounds: %v", m.enforcedClients)
	}

	// The monthly reset brings the inbound over its quota back.
	m.applyScheduledTrafficResets()
	if m.state.Inbounds[0].Traffic.Up != 0 || m.enforcedClients["quota"] || !m.enforcedClients["expired"] {
		t.Fatalf("after the reset: traffic %+v, enforced %v", m.state.Inbounds[0].Traffic, m.enforcedClients)
	}

	// Subscriptions leave out the nodes of an inbound that is used up or expired.
	if !inboundDepleted(m.state.Inbounds[1], time.Now().UnixMilli()) || inboundDepleted(m.state.Inbounds[3], time.Now().UnixMilli()) {
		t.Fatal("inboundDepleted")
	}
}

func TestSubscriptionLeavesOutDepletedInbounds(t *testing.T) {
	client := Client{Name: "alice", Username: "alice", Password: "one", Enabled: true}
	inbound := func(name string, port int) Inbound {
		return Inbound{Name: name, Type: "shadowsocks", Port: port, Enabled: true, Cipher: "aes-256-gcm"}
	}
	used := inbound("used", 1002)
	used.Total, used.Traffic = 10, TrafficStats{Down: 10}
	expired := inbound("expired", 1003)
	expired.ExpiryTime = time.Now().Add(-time.Minute).UnixMilli()
	snapshot := subscriptionSnapshot{Settings: Settings{ClashPath: "/clash"}, Username: "alice", Clients: []subscriptionClient{
		{Inbound: inbound("live", 1001), Client: client}, {Inbound: used, Client: client}, {Inbound: expired, Client: client},
	}}
	if got := subscriptionLinks(snapshot, "example.com"); len(got) != 1 {
		t.Fatalf("subscription node count = %d, want 1 (only the live inbound)", len(got))
	}
}
