---
name: personal-computing-space
description: Use the AgentServer Loom MCP tools to discover and operate resources, capability packages, activations, services, and distributed tasks through the public Loom Observer.
---

# Loom Personal Computing Space

Use the AgentServer MCP tools for every Loom operation:

- `loom_inventory`: discover resources, packages, activations, Slaves and Runs.
- `loom_describe`: DESCRIBE an exact GRIP URI and inspect metadata, offers, permissions, interaction mode, and schema references.
- `loom_invoke`: INVOKE an exact action URI with JSON input after DESCRIBE and schema validation.
- `loom_stream`: invoke an exact byte-stream action discovered from DESCRIBE.

Do not guess a resource URI, action URI, input field, dependency, provider, or lifecycle state. Keep the exact URIs returned by Loom. Never call Loom internal `/mcp`, `/internal/v1/*`, `/worker/v1/*`, or Driver/Slave private endpoints.

## Required workflow

1. Start with `loom_inventory`. Include pending or degraded resources in the assessment.
2. Call `loom_describe` for the selected resource or activation.
3. When the relationship, dependency, activation, authentication, or action semantics are unclear, find the exact built-in `how-to-use` action in the returned offers and call it with `loom_invoke` using `{}`. Loom returns the package `HOW_TO_USE.md` through `documentation`/`usage_markdown` when it is published.
4. Read the exact input and output schemas and the action's permissions, effects, readiness, retry, cleanup, and replay-safety declarations.
5. For a named service, use the discovered `check-package`, `activate-package`, `auth-start`, and lifecycle actions. Wait for the declared ready/authenticated state, then DESCRIBE the same activation again before business calls.
6. Choose direct execution for simple reads and exploration when the action advertises it. Choose a complete Driver plan for durable work, scheduling, recovery, or multiple dependent nodes. Save returned Run/Plan/submission references and poll the same returned URI.
7. Do not retry an uncertain direct call unless the action's idempotency contract permits it. A GRIP request ID is not a business idempotency key.
8. Preserve resource, activation, action, schema, Run, and content references as evidence. Stop when metadata or schemas are missing or conflicting instead of inferring behavior.

## Authentication and safety

Authentication belongs to the activation's declared authentication flow and operator-provided credential profile. Never put cookies, bearer tokens, private keys, kubeconfigs, or credential files in action input, skill text, prompts, or content. Activation credentials are separate from public attributes.

A successful HTTP response is not proof that the business goal succeeded. Verify the returned business result, Run state, output schema, and declared success conditions. For `awaiting_decision`, present the evidence and obtain the user's explicit decision before continuing.

Read the bundled references when the task involves service bindings, authentication, MCUE attributes, capability descriptors, existing activations, reliable workflows, or capability scope.
