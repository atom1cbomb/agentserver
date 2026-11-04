# Loom with the legacy Helm deployment

This guide applies to the legacy (non-v2) AgentServer Helm chart. Docker and
Helm use the same `codex-app-gateway` binary: it exposes the Loom MCP tools and
installs the `personal-computing-space` skill into each temporary `CODEX_HOME`.
Helm does not read files from a host Loom checkout.

## 1. Build and publish the gateway image

Build `Dockerfile.codex-app-gateway` from the commit containing the Loom
integration and publish it to a registry visible to the cluster. The image must
contain the embedded skill. Set the chart image to that tag:

```yaml
codexAppGateway:
  enabled: true
  image:
    repository: ghcr.io/your-org/agentserver/codex-app-gateway
    tag: loom-<commit>
```

Updating only Helm values while keeping an old image cannot install the skill.

## 2. Create the Loom token Secret

Create this Secret in the same namespace as the Helm release. Keep the token
out of `values.yaml` and ConfigMaps:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: agentserver-loom
type: Opaque
stringData:
  grip-token: "<Loom GRIP token>"
```

```sh
kubectl apply -f agentserver-loom-secret.yaml
```

## 3. Enable the bridge

Add these values to the release (the S3, API key, and internal secret settings
required by the existing gateway remain required):

```yaml
codexAppGateway:
  loom:
    enabled: true
    observerURL: http://loom-observer:8080
    authority: loom.local
    workspace: workspace-default
    existingSecret: agentserver-loom
```

The chart injects the following only when `loom.enabled` is true:

* `CXG_LOOM_OBSERVER_URL`
* `CXG_LOOM_AUTHORITY`
* `CXG_LOOM_WORKSPACE`
* `CXG_LOOM_TOKEN` (from Secret key `grip-token`)

The token is passed through the gateway process environment to `env-mcp`; it is
not written to per-thread `config.toml` or exposed as an MCP argument.

## 4. Verify

Render or inspect the Deployment and confirm the four variables are present:

```sh
helm template agentserver deploy/helm/agentserver \
  -n agentserver -f values.yaml | rg 'CXG_LOOM|grip-token'
kubectl -n agentserver rollout status deploy/agentserver-codex-app-gateway
```

With `enabled: false`, no `CXG_LOOM_*` variables are rendered. With
`enabled: true`, Helm fails rendering if `observerURL` or `existingSecret` is
missing. The referenced Secret must contain the `grip-token` key; Kubernetes
will reject the Pod if it does not.

Once the gateway is running, the Codex agent sees the embedded skill and can
use `loom_inventory`, `loom_describe`, `loom_invoke`, and `loom_stream`. The
skill discovers each capability package's native `how-to-use` action from Loom
dynamically; no separate `loom_how_to_use` tool is required.
