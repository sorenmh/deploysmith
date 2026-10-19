package store

import (
	"testing"

	"github.com/sorenmh/deploysmith/internal/smithd/db/dbtest"
)

func TestPushSubscriptions(t *testing.T) {
	database := dbtest.Open(t)
	subs := NewPushSubscriptionStore(database)

	if err := subs.Save(PushSubscription{Endpoint: "https://push.example/1", P256dh: "k1", Auth: "a1", User: "me@example.com"}); err != nil {
		t.Fatal(err)
	}
	// Subscribing again from the same browser replaces the keys
	if err := subs.Save(PushSubscription{Endpoint: "https://push.example/1", P256dh: "k2", Auth: "a2", User: "me@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := subs.Save(PushSubscription{Endpoint: "https://push.example/2", P256dh: "k3", Auth: "a3"}); err != nil {
		t.Fatal(err)
	}

	list, err := subs.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].P256dh != "k2" {
		t.Fatalf("unexpected subscriptions: %+v", list)
	}

	if err := subs.Delete("https://push.example/1"); err != nil {
		t.Fatal(err)
	}
	if list, _ = subs.List(); len(list) != 1 || list[0].Endpoint != "https://push.example/2" {
		t.Fatalf("unexpected subscriptions after delete: %+v", list)
	}
}
