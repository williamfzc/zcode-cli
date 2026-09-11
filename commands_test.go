// Unit tests for ssh list contract validation, including the omitted-vs-null
// privateKeyPath shapes observed on the live app.

package main

import (
	"encoding/json"
	"testing"
)

func TestValidateSSHListAcceptsOmittedPrivateKey(t *testing.T) {
	raw := json.RawMessage(`[{"alias":"dev","host":"example.com","port":22,"username":"alice","source":"/Users/alice/.ssh/config"}]`)
	entries, err := validateSSHList(raw)
	if err != nil {
		t.Fatalf("validateSSHList returned error: %v", err)
	}
	if len(entries) != 1 || entries[0].PrivateKeyPath != nil {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestValidateSSHListAcceptsExplicitNullPrivateKey(t *testing.T) {
	raw := json.RawMessage(`[{"alias":"dev","host":"example.com","port":22,"username":"alice","privateKeyPath":null,"source":"/Users/alice/.ssh/config"}]`)
	if _, err := validateSSHList(raw); err != nil {
		t.Fatalf("validateSSHList returned error: %v", err)
	}
}

func TestValidateSSHListRejectsInvalidPort(t *testing.T) {
	raw := json.RawMessage(`[{"alias":"dev","host":"example.com","port":0,"username":"alice","source":"/Users/alice/.ssh/config"}]`)
	if _, err := validateSSHList(raw); err == nil {
		t.Fatal("expected invalid port to be rejected")
	}
}

func TestValidateSSHListRejectsMissingRequiredString(t *testing.T) {
	raw := json.RawMessage(`[{"alias":"dev","host":"example.com","port":22,"username":"","source":"/Users/alice/.ssh/config"}]`)
	if _, err := validateSSHList(raw); err == nil {
		t.Fatal("expected empty username to be rejected")
	}
}

func TestValidateSSHListRejectsNonArray(t *testing.T) {
	raw := json.RawMessage(`{"alias":"dev"}`)
	if _, err := validateSSHList(raw); err == nil {
		t.Fatal("expected non-array to be rejected")
	}
}
