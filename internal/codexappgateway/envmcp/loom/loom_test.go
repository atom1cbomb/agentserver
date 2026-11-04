package loom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInventoryUsesPublicGRIP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/grip" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["method"] != "DESCRIBE" && req["method"] != "INVOKE" {
			t.Fatalf("method = %v", req["method"])
		}
		if req["target"] != "grip://loom.local/workspaces/workspace-default" {
			t.Fatalf("target = %v", req["target"])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"version": "grip.v1", "request_id": req["request_id"], "kind": "reply", "body": map[string]any{"offers": []any{map[string]any{"action": map[string]any{"uri": "grip://loom.local/workspaces/workspace-default/actions/list-resources/v1"}}}}})
	}))
	defer ts.Close()
	c := NewClient(ts.URL, "loom.local", "workspace-default", "token")
	result, err := (&inventoryTool{c}).Call(context.Background(), nil)
	if err != nil || result.IsError {
		t.Fatalf("inventory failed: %v %+v", err, result)
	}
}

func TestNewToolsSurface(t *testing.T) {
	tools := NewTools(NewClient("http://observer", "loom.local", "workspace-default", "token"))
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool.Name()] = true
	}
	for _, name := range []string{"loom_inventory", "loom_describe", "loom_invoke", "loom_stream"} {
		if !got[name] {
			t.Fatalf("missing %s", name)
		}
	}
}
