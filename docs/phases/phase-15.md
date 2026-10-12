# Phase 15 — Cloud Providers

`cloudprovider` provides an injected, no-network cloud job boundary: opaque runtime
credentials, submit/poll/cancel/download operations, bounded retry/rate policy, explicit
cost/latency metadata, and checksummed controlled-sink downloads. No cloud SDK, endpoint,
credential persistence, paid request, or automatic polling loop exists.

Luna and Terra independently approved with no findings. Terra replaces Luna6 because the
available Luna6 alias resolved to the same model as Luna, while the phase gate requires
distinct reviewer models. Final test/race/vet/build checks passed.
