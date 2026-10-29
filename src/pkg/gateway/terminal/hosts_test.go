package terminal

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestHostStorePersistsHostMetadataWithoutCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "terminal", "hosts.json")
	store, err := NewHostStore(path)
	if err != nil {
		t.Fatal(err)
	}

	host, err := store.Upsert(SavedHost{
		Host: "10.10.10.16", Port: 22, Username: "root", Fingerprint: "SHA256:test",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.Upsert(SavedHost{
		Host: "10.10.10.16", Port: 22, Username: "root", Fingerprint: "SHA256:updated",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != host.ID {
		t.Fatalf("host ID changed on update: %q != %q", updated.ID, host.ID)
	}

	reloaded, err := NewHostStore(path)
	if err != nil {
		t.Fatal(err)
	}
	hosts := reloaded.List()
	if len(hosts) != 1 || hosts[0].ID != host.ID || hosts[0].Fingerprint != "SHA256:updated" {
		t.Fatalf("unexpected persisted host list: %+v", hosts)
	}

	if err := reloaded.Remove(host.ID); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Remove(host.ID); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("expected missing host error, got %v", err)
	}
}
