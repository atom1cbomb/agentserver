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

func TestDescribeResolvesActionSchemas(t *testing.T) {
	const inputRef = "grip://loom.local/workspaces/workspace-default/content/input"
	const outputRef = "grip://loom.local/workspaces/workspace-default/content/output"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		method, _ := req["method"].(string)
		target, _ := req["target"].(string)
		if method == "DESCRIBE" && target == "grip://loom.local/workspaces/workspace-default/actions/run" {
			json.NewEncoder(w).Encode(map[string]any{"kind": "reply", "body": map[string]any{
				"resource": map[string]any{"metadata": map[string]any{
					"arguments_schema": map[string]any{"resource_id": inputRef},
					"result_schema":    map[string]any{"resource_id": outputRef},
				}},
			}})
			return
		}
		if method == "INVOKE" && target == inputRef {
			json.NewEncoder(w).Encode(map[string]any{"kind": "reply", "body": map[string]any{"content": map[string]any{"required": []string{"ProjectId", "ImageId"}}}})
			return
		}
		if method == "INVOKE" && target == outputRef {
			json.NewEncoder(w).Encode(map[string]any{"kind": "reply", "body": map[string]any{"content": map[string]any{"required": []string{"Instances"}}}})
			return
		}
		t.Fatalf("unexpected GRIP request: %v", req)
	}))
	defer ts.Close()
	result, err := (&describeTool{c: NewClient(ts.URL, "loom.local", "workspace-default", "token")}).Call(context.Background(), json.RawMessage(`{"target":"grip://loom.local/workspaces/workspace-default/actions/run"}`))
	if err != nil || result.IsError {
		t.Fatalf("describe failed: %v %+v", err, result)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["resolved_input_schema"]; !ok {
		t.Fatalf("resolved input schema missing: %s", result.Content[0].Text)
	}
	if _, ok := body["resolved_output_schema"]; !ok {
		t.Fatalf("resolved output schema missing: %s", result.Content[0].Text)
	}
}
