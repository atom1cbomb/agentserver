package loom

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/agentserver/agentserver/internal/envtools/tools"
	"nhooyr.io/websocket"
)

// Client exposes Loom's public GRIP data plane as MCP tools. It deliberately
// keeps the protocol generic: DESCRIBE supplies the exact action and schema,
// while INVOKE forwards the caller's validated input to the returned target.
type Client struct {
	baseURL   string
	authority string
	workspace string
	token     string
	http      *http.Client
	seq       atomic.Uint64
}

func NewClient(baseURL, authority, workspace, token string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), authority: authority, workspace: workspace, token: token,
		http: &http.Client{Timeout: 10 * time.Minute}}
}

func (c *Client) targetRoot() string {
	return fmt.Sprintf("grip://%s/workspaces/%s", c.authority, c.workspace)
}

func (c *Client) call(ctx context.Context, method, target, action string, input any) (map[string]any, error) {
	if c.baseURL == "" || c.authority == "" || c.workspace == "" || c.token == "" {
		return nil, fmt.Errorf("loom is not configured")
	}
	c.seq.Add(1)
	body := map[string]any{"version": "grip.v1", "request_id": fmt.Sprintf("agentserver-loom-%d", c.seq.Load()), "method": method,
		"auth": map[string]any{"scheme": "bearer", "credential": c.token}, "target": target,
		"accepted_protocols": []string{"loom-http-json.v1"}}
	if method == "INVOKE" {
		body["action"] = action
		body["input"] = input
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/.well-known/grip", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("loom request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loom transport status %d", resp.StatusCode)
	}
	var reply struct {
		Kind         string         `json:"kind"`
		Body         map[string]any `json:"body"`
		ErrorCode    string         `json:"error_code"`
		ErrorMessage string         `json:"error_message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, fmt.Errorf("loom reply: %w", err)
	}
	if reply.Kind == "error" {
		return nil, fmt.Errorf("loom %s: %s", reply.ErrorCode, reply.ErrorMessage)
	}
	if reply.Kind != "reply" || reply.Body == nil {
		return nil, fmt.Errorf("invalid loom reply")
	}
	return reply.Body, nil
}

type inventoryTool struct{ c *Client }

func (*inventoryTool) Name() string { return "loom_inventory" }
func (*inventoryTool) Description() string {
	return "List Loom resources, capability packages, activations, Slaves and Runs in the current workspace. Start here before using any Loom action."
}
func (*inventoryTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *inventoryTool) Call(ctx context.Context, _ json.RawMessage) (tools.MCPCallToolResult, error) {
	d, err := t.c.call(ctx, "DESCRIBE", t.c.targetRoot(), "", nil)
	if err != nil {
		return errorResult(err), nil
	}
	offers, _ := d["offers"].([]any)
	var action string
	for _, v := range offers {
		if o, ok := v.(map[string]any); ok {
			if a, ok := o["action"].(map[string]any); ok && strings.HasSuffix(fmt.Sprint(a["uri"]), "/actions/list-resources/v1") {
				action = fmt.Sprint(a["uri"])
				break
			}
		}
	}
	if action == "" {
		return errorResult(fmt.Errorf("Loom workspace has no list-resources action")), nil
	}
	out, err := t.c.call(ctx, "INVOKE", t.c.targetRoot(), action, map[string]any{})
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(out), nil
}

type describeTool struct{ c *Client }

func (*describeTool) Name() string { return "loom_describe" }
func (*describeTool) Description() string {
	return "DESCRIBE an exact Loom GRIP URI and return its offers, action metadata and schema references. Never guess a URI or action."
}
func (*describeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"}},"required":["target"]}`)
}
func (t *describeTool) Call(ctx context.Context, args json.RawMessage) (tools.MCPCallToolResult, error) {
	var a struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Target == "" {
		return errorResult(fmt.Errorf("target is required")), nil
	}
	out, err := t.c.call(ctx, "DESCRIBE", a.Target, "", nil)
	if err != nil {
		return errorResult(err), nil
	}
	// Action DESCRIBE responses carry content-digest references for the
	// argument and result schemas. Resolve those references here so an agent
	// does not mistake a schema URI for the schema itself and omit required
	// fields. The original GRIP description remains intact.
	resolveDescriptionSchemas(ctx, t.c, out)
	return jsonResult(out), nil
}

func resolveDescriptionSchemas(ctx context.Context, c *Client, description map[string]any) {
	resource, _ := description["resource"].(map[string]any)
	metadata, _ := resource["metadata"].(map[string]any)
	resolveActionSchemas(ctx, c, metadata)
	// Some Loom descriptions expose actions directly under the resource
	// metadata, while others expose them in a nested service_access object.
	if serviceAccess, ok := metadata["service_access"].(map[string]any); ok {
		resolveActionSchemas(ctx, c, serviceAccess)
	}
	// An exact action description stores its schema refs on metadata itself.
	// Package and activation descriptions store action maps in metadata.actions;
	// both forms must be resolved for the agent to see required fields.
	resolveActionSchemas(ctx, c, resource)
	resolveActionSchemas(ctx, c, description)
}

func resolveActionSchemas(ctx context.Context, c *Client, object map[string]any) {
	if object == nil {
		return
	}
	resolveOneActionSchemas(ctx, c, object)
	actions, _ := object["actions"].([]any)
	for _, value := range actions {
		if action, ok := value.(map[string]any); ok {
			resolveOneActionSchemas(ctx, c, action)
		}
	}
}

func resolveOneActionSchemas(ctx context.Context, c *Client, action map[string]any) {
	for _, item := range []struct {
		refKey string
		outKey string
	}{
		{refKey: "arguments_schema", outKey: "resolved_input_schema"},
		{refKey: "result_schema", outKey: "resolved_output_schema"},
	} {
		ref, _ := action[item.refKey].(map[string]any)
		contentURI, _ := ref["resource_id"].(string)
		if contentURI == "" {
			continue
		}
		content, err := c.readContent(ctx, contentURI)
		if err != nil {
			action[item.outKey+"_error"] = err.Error()
			continue
		}
		action[item.outKey] = content
	}
}

func (c *Client) readContent(ctx context.Context, contentURI string) (any, error) {
	out, err := c.call(ctx, "INVOKE", contentURI, c.targetRoot()+"/actions/read/v1", map[string]any{})
	if err != nil {
		return nil, err
	}
	content, ok := out["content"]
	if !ok {
		return nil, fmt.Errorf("Loom content response has no content")
	}
	return content, nil
}

type invokeTool struct{ c *Client }

func (*invokeTool) Name() string { return "loom_invoke" }
func (*invokeTool) Description() string {
	return "INVOKE an exact Loom action URI with JSON input after DESCRIBE and schema validation. This is the complete Loom public operation path; follow the action's permissions, effects, authentication and idempotency rules."
}
func (*invokeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"action":{"type":"string"},"input":{"type":"object"}},"required":["target","action","input"]}`)
}
func (t *invokeTool) Call(ctx context.Context, args json.RawMessage) (tools.MCPCallToolResult, error) {
	var a struct {
		Target, Action string
		Input          map[string]any `json:"input"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Target == "" || a.Action == "" || a.Input == nil {
		return errorResult(fmt.Errorf("target, action and input are required")), nil
	}
	out, err := t.c.call(ctx, "INVOKE", a.Target, a.Action, a.Input)
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(out), nil
}

type streamTool struct{ c *Client }

func (*streamTool) Name() string { return "loom_stream" }
func (*streamTool) Description() string {
	return "Open a Loom byte-stream action discovered from DESCRIBE. Optional write_base64 is sent after stream-open; returned binary data is base64 encoded. Use the exact stream action and its schema."
}
func (*streamTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"action":{"type":"string"},"input":{"type":"object"},"write_base64":{"type":"string"}},"required":["target","action","input"]}`)
}
func (t *streamTool) Call(ctx context.Context, args json.RawMessage) (tools.MCPCallToolResult, error) {
	var a struct {
		Target      string         `json:"target"`
		Action      string         `json:"action"`
		Input       map[string]any `json:"input"`
		WriteBase64 string         `json:"write_base64"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Target == "" || a.Action == "" || a.Input == nil {
		return errorResult(fmt.Errorf("target, action and input are required")), nil
	}
	d, err := t.c.call(ctx, "DESCRIBE", a.Target, "", nil)
	if err != nil {
		return errorResult(err), nil
	}
	offers, _ := d["offers"].([]any)
	var endpoint string
	for _, v := range offers {
		o, _ := v.(map[string]any)
		act, _ := o["action"].(map[string]any)
		if fmt.Sprint(act["uri"]) != a.Action {
			continue
		}
		bs, _ := o["bindings"].([]any)
		for _, b := range bs {
			bm, _ := b.(map[string]any)
			if bm["protocol"] == "loom-wss-stream.v1" {
				endpoint = fmt.Sprint(bm["endpoint"])
				break
			}
		}
	}
	if endpoint == "" {
		return errorResult(fmt.Errorf("stream binding not offered for action")), nil
	}
	request := map[string]any{"version": "grip.v1", "request_id": fmt.Sprintf("agentserver-loom-%d", t.c.seq.Add(1)), "method": "INVOKE", "auth": map[string]any{"scheme": "bearer", "credential": t.c.token}, "target": a.Target, "action": a.Action, "input": a.Input, "accepted_protocols": []string{"loom-wss-stream.v1"}}
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{Subprotocols: []string{"loom-wss-stream.v1"}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return errorResult(err), nil
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, mustJSON(request)); err != nil {
		return errorResult(err), nil
	}
	kind, data, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageText {
		if err == nil {
			err = fmt.Errorf("invalid stream-open")
		}
		return errorResult(err), nil
	}
	var open struct {
		Kind         string `json:"kind"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	if err = json.Unmarshal(data, &open); err != nil || open.Kind != "stream-open" {
		if err == nil {
			err = fmt.Errorf("loom %s: %s", open.ErrorCode, open.ErrorMessage)
		}
		return errorResult(err), nil
	}
	if a.WriteBase64 != "" {
		payload, e := base64.StdEncoding.DecodeString(a.WriteBase64)
		if e != nil {
			return errorResult(e), nil
		}
		if err = conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
			return errorResult(err), nil
		}
	}
	var out bytes.Buffer
	for {
		kind, data, e := conn.Read(ctx)
		if e != nil {
			if websocket.CloseStatus(e) != websocket.StatusNormalClosure {
				return errorResult(e), nil
			}
			break
		}
		if kind != websocket.MessageBinary {
			break
		}
		if out.Len()+len(data) > 32<<20 {
			return errorResult(fmt.Errorf("stream output exceeds 32 MiB")), nil
		}
		out.Write(data)
	}
	return jsonResult(map[string]any{"data_base64": base64.StdEncoding.EncodeToString(out.Bytes())}), nil
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func NewTools(c *Client) []tools.Tool {
	return []tools.Tool{&inventoryTool{c}, &describeTool{c}, &invokeTool{c}, &streamTool{c}}
}
func jsonResult(v any) tools.MCPCallToolResult {
	b, _ := json.Marshal(v)
	return tools.MCPCallToolResult{Content: []tools.MCPToolContent{{Type: "text", Text: string(b)}}}
}
func errorResult(err error) tools.MCPCallToolResult {
	return tools.MCPCallToolResult{Content: []tools.MCPToolContent{{Type: "text", Text: "Loom error: " + err.Error()}}, IsError: true}
}
