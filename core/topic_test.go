package core

import (
	"context"
	"strings"
	"testing"
)

type donation struct {
	From   string
	Amount int
}

var donationReceived = Topic[donation]{Name: "donation.received"}

func TestTypedTopicRoundTrip(t *testing.T) {
	m, bus, _ := newTestManager(t)
	var got []donation
	mod := &testModule{name: "donations", start: func(h *Host) error {
		return Subscribe(h, donationReceived, func(_ context.Context, d donation) error {
			got = append(got, d)
			return nil
		})
	}}
	if err := m.Load(mod); err != nil {
		t.Fatal(err)
	}
	if err := Emit(context.Background(), bus, donationReceived, donation{"ann", 5}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (donation{"ann", 5}) {
		t.Fatalf("got %+v", got)
	}
}

func TestTypedTopicRejectsWrongPayload(t *testing.T) {
	m, bus, _ := newTestManager(t)
	called := false
	mod := &testModule{name: "donations", start: func(h *Host) error {
		return Subscribe(h, donationReceived, func(context.Context, donation) error { called = true; return nil })
	}}
	if err := m.Load(mod); err != nil {
		t.Fatal(err)
	}
	err := bus.Publish(context.Background(), Event{Topic: donationReceived.Name, Payload: "not a donation"})
	if err == nil || !strings.Contains(err.Error(), "payload is string") || called {
		t.Fatalf("err %v, called %v", err, called)
	}
}

func TestEmitFromHost(t *testing.T) {
	m, _, _ := newTestManager(t)
	received := make(chan Message, 1)
	listener := &testModule{name: "listener", start: func(h *Host) error {
		return Subscribe(h, ChatMessage, func(_ context.Context, msg Message) error { received <- msg; return nil })
	}}
	speaker := &testModule{name: "speaker", start: func(h *Host) error {
		return Emit(h.Context(), h, ChatMessage, Message{Text: "hi"})
	}}
	if err := m.Load(listener); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(speaker); err != nil {
		t.Fatal(err)
	}
	if msg := <-received; msg.Text != "hi" {
		t.Fatalf("got %+v", msg)
	}
}
