# Providers and execution boundaries

The current local/cloud packages supply validated, injected adapter boundaries.
Their tests use deterministic fixtures. No vendor model, live cloud transport,
provider installer, or credential discovery is shipped. Configuring the CLI does
not enable generation: its JSON schema is presentation-only.

## Contracts and routing

[`provider.Provider`](../provider/provider.go) reports a stable nonsecret ID and
context-aware capabilities. Execution is expressed by separate interfaces such as
`decision.Provider`; a capability tag alone is not an execution implementation.

The registry starts empty and registered providers start with UNKNOWN health.
Routing requires explicitly HEALTHY status, all requested tags, and applicable
cost, latency, locality, and hardware constraints. Local-only is the default;
remote eligibility must be explicit. Eligible preferences precede deterministic,
local non-deterministic, and remote candidates, with stable ID tie-breaking.
Routing produces advice, not a resource lease, model load, or spending authority.

## Offline sample provider

[`decision.NewFixtureProvider`](../decision/fixture.go) is a concrete sample:
a copied per-kind table of decision results, ignoring request input. It declares
`semantic-decision` and `deterministic` capabilities and never invokes a model,
network, or process. The Decision Engine validates scores and applies its own
threshold policy.

Run its ownership/capability test:

```sh
go test -count=1 -v ./decision -run '^TestFixtureProviderTableOwnershipAndCapabilities$'
```

Authorized adapter developers can inspect
[`localprovider/localprovider_test.go`](../localprovider/localprovider_test.go)
and [`cloudprovider/cloudprovider_test.go`](../cloudprovider/cloudprovider_test.go)
for injected runner/transport examples. These are protocol fixtures, not working
vendor recipes. Modification or derivative work requires written authorization
under [LICENSE](../LICENSE).

## Local adapters

`localprovider` supports generation, semantic decision, and ASR boundaries over
caller-injected dependencies. A descriptor binds explicit model/profile/adapter
revisions and declared workload/hardware requirements. The generation adapter
receives a trusted `worker.CommandSpec`, limits, and exact draft/final workload
configurations. Request parameters cannot choose executables, arguments,
environment, resource limits, or model profiles.

The caller owns admission, resource leasing, model load/use/unload, and cleanup.
The adapter invokes its injected runner; it has no default runtime or process
launcher of its own. A successful protocol reply does not prove media quality.
If stop cannot be proven, lifecycle cleanup ownership is returned and the caller
must retain or quarantine the reservation rather than refund live resources.

## Cloud adapters

`cloudprovider` takes an injected `CredentialSource`, `Transport`, explicit bounded
policy, and staging `Sink`. It supports submit, caller-driven polling, optional
remote cancellation, and size/SHA-256-verified download to that sink. It does not
make network requests itself, sleep between polls, or start polling goroutines.

Opaque credentials are passed only to the injected transport; credential formatting
is redacted and JSON serialization is rejected. Transport/source implementations
must not log, persist, or retain the underlying credential. Logical IDs are public
handles, never bearer tokens or URLs. The transport owns submission idempotency.
Context cancellation stops a local attempt and does not prove remote cancellation.
Download integrity is separate from media validation and artifact-cache publication.

Offline adapter verification:

```sh
go test -count=1 ./localprovider ./cloudprovider
```

These tests do not exercise live inference, service authentication, billing, or
remote generation latency.

## Pipeline boundary

[`pipeline.Run`](../pipeline/pipeline.go) executes the deterministic path through
injected planning, analysis, preview, artifact/checkpoint, composition, and final
validation dependencies. Local, cloud, and mixed strategies produce typed pending
approval results. The declared `ApprovalGate` and `Executor` interfaces are not
called by `Run`; a pending result is not permission or an available CLI action to
start live execution.

## Local hardware limitations

- The reference development target is Apple Silicon with 16 GB unified memory.
  It is not a claim that a particular AI model runs or fits on that machine.
- RAM, CPU, accelerator backend, and workload bounds are explicit facts. Metal
  declarations neither detect hardware nor imply CUDA compatibility. Unified
  memory must be included in declared RAM requirements.
- Unknown or unbounded local profiles are not loadable. Reservations use declared
  peak requirements and safety margins; sampled process RSS is not a kernel-enforced
  instantaneous memory cap or protection against an uncooperative process.
- The worker runner owns macOS/Linux process groups and monitors their lifecycle.
  Workers must not daemonize or escape the group. Other platforms fail explicitly
  for this runner, even when the CLI cross-compiles.
- No real-model load/startup timing or model-specific Mac benchmark is established
  by the offline fixtures.

See [ARCHITECTURE.md](../ARCHITECTURE.md), [PHASES.md](../PHASES.md), and
[dependencies](dependencies.md) for the detailed contracts and native requirements.
