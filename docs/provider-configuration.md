# Replaceable Provider Configuration

Set `CODE_AGENT_PROVIDER_CONFIG` to a local file before starting
`python -m orchestrator.server`. Set `CODE_AGENT_PROVIDER_PROFILE` to select a
named profile, or a 1-based profile number for legacy notes. Restart the Python
orchestrator after changing files. Live in-flight runs keep their original
configuration until restarted; changing credentials does not rewrite Sessions.

The launch helper accepts the same settings:

```powershell
./scripts/start-interview.ps1 -ProviderConfig 'C:\path\provider.json' -ProviderProfile primary
```

Add `-RestartOrchestrator` to apply a different profile to an already-running
workbench. The helper verifies the listener is a Python `orchestrator.server`
before stopping it. Avoid switching while a run is active: changing provider
mid-run can interrupt generation, and recovery must preserve the original task.

Use `configs/provider.example.json` as the standard schema. Keep the real file
outside the repository. The loader also accepts a JSON env map, Claude-style
`{"env": {...}}` settings, and legacy notes containing a bare JSON field fragment
followed by prose and complete JSON objects. Legacy files default to the last
object; use `-ProviderProfile 1` for the first fragment.

An explicit configuration file replaces provider-related environment settings
after `.env.local` is loaded. Validation completes before any environment update.
Without a file, the existing environment-based startup remains supported.

Supported protocols: `LLM_PROVIDER=anthropic` or `openai`. The protocol is
independent of the model vendor: a Grok model behind an Anthropic-compatible
endpoint uses `anthropic`.

| Fields | Meaning |
| --- | --- |
| `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` | Anthropic-compatible credential; API key takes precedence if both are supplied |
| `OPENAI_API_KEY` | OpenAI-compatible credential |
| `<PREFIX>_BASE_URL`, `<PREFIX>_MODEL` | Endpoint and actual model identifier |
| `<PREFIX>_TIMEOUT`, `<PREFIX>_MAX_RETRIES`, `<PREFIX>_MAX_TOKENS` | Positive request timeout, nonnegative integer retries and token limit |
| `ANTHROPIC_THINKING_BUDGET_TOKENS` | Thinking budget for compatible models |
| `THINKING_ENABLED` | Boolean, defaults to false for file-based profiles |
| `MODEL_FAST` | Optional separate fast route; defaults to disabled |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` | Process-local proxy settings; loopback bypass defaults are supplied |

The model can also be specified with a root `model` field if the env map has no
protocol-specific model field. Other application settings, including
`enabledPlugins`, `permissions`, `effortLevel`, display names and Claude model
aliases, are ignored: they are not capabilities of this runtime.

Validate without printing credentials:

```powershell
python -m orchestrator.config.provider_file 'C:\path\provider.json' --profile primary
```

Validation reports protocol and accepted field names only. It does not establish
provider connectivity, account balance, or model availability.
