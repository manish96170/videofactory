// Package observability provides bounded, dependency-free in-process logs,
// metrics, and trace correlation for callers to wire to their own sinks.
//
// It performs no persistence, network/cloud export, scheduling, or payload
// capture. Log codes and metric labels must be controlled, non-sensitive
// operational classifications; arbitrary text and payload values are not
// representable as log fields.
package observability
