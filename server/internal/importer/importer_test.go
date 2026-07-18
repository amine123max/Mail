package importer

import (
	"testing"

	"github.com/amine123max/Mail/server/internal/model"
)

func TestParseTabSeparated(t *testing.T) {
	result := Parse("user@example.com\tpass\tclient-id\trefresh-token")
	if len(result.Errors) != 0 || len(result.Accounts) != 1 || result.Accounts[0].Email != "user@example.com" {
		t.Fatalf("unexpected parse result: %#v", result)
	}
}

func TestParseHyphenTokenTail(t *testing.T) {
	result := Parse("user@example.com----pass----client-id----token----tail")
	if len(result.Errors) != 0 || result.Accounts[0].RefreshToken != "token----tail" {
		t.Fatalf("unexpected parse result: %#v", result)
	}
}

func TestSerialize(t *testing.T) {
	content := Serialize([]model.AccountCredentials{{Email: "first@example.com", Password: "pass", ClientID: "client", RefreshToken: "refresh----tail"}})
	if content != "first@example.com----pass----client----refresh----tail" {
		t.Fatalf("unexpected export: %q", content)
	}
}
