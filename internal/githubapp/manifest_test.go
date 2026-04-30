package githubapp

import (
	"encoding/json"
	"testing"
)

func TestBuildManifest_RequiredFields(t *testing.T) {
	m := BuildManifest(ManifestInput{
		Name:        "cronfoundry-tng",
		CallbackURL: "http://localhost:8765",
	})
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["name"] != "cronfoundry-tng" {
		t.Errorf("name = %v, want cronfoundry-tng", got["name"])
	}
	if got["url"] != "http://localhost:8765" {
		t.Errorf("url = %v", got["url"])
	}
	if got["redirect_url"] != "http://localhost:8765/callback" {
		t.Errorf("redirect_url = %v", got["redirect_url"])
	}
	if got["setup_url"] != "http://localhost:8765/installed" {
		t.Errorf("setup_url = %v", got["setup_url"])
	}
	if got["public"] != false {
		t.Errorf("public = %v, want false", got["public"])
	}
	perms, _ := got["default_permissions"].(map[string]any)
	for k, want := range map[string]string{
		"contents":        "write",
		"issues":          "write",
		"metadata":        "read",
		"email_addresses": "read",
	} {
		if perms[k] != want {
			t.Errorf("permissions[%s] = %v, want %s", k, perms[k], want)
		}
	}
	events, _ := got["default_events"].([]any)
	if len(events) != 1 || events[0] != "push" {
		t.Errorf("default_events = %v, want [push]", events)
	}
	hook, _ := got["hook_attributes"].(map[string]any)
	if hook["url"] != "http://localhost:8765/webhook" {
		t.Errorf("hook url = %v", hook["url"])
	}
}
