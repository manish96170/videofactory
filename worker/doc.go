// Package worker runs trusted local runtimes with bounded JSON I/O and explicit
// process-family ownership. The caller retains its full model resource lease
// until RunResult.Stopped or CleanupHandle.RetryStop returns true. This package
// never acquires or releases resource leases.
//
// Runtimes must not daemonize, change process groups, or escape their owned group.
// This is not a sandbox for malicious code. RAM reservations and periodic native
// RSS checks are not an instantaneous kernel ceiling; CPU throttling and VRAM
// containment are not provided. Zombie descendants have no live RSS and are
// distinguished from live processes; only the direct child is reapable by this
// parent. The kernel/adoptive parent handles orphaned descendant zombies.
//
// Injected monitors and clocks must be concurrency-safe. Monitors must honor
// context cancellation and truthfully report native group membership, not worker
// reply metrics. A noncooperative callback prevents stop proof and remains owned
// by the returned cleanup handle; retry never creates a second pending query.
// Callers must not mutate requests/configuration during Run's initial snapshot.
package worker
