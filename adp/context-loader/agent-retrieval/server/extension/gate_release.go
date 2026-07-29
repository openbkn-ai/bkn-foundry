//go:build !ee_dev

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package extension

// DefaultGate is the gate a production build installs.
//
// The shipped form reads the signed .lic text that bkn-safe (the cluster
// license server) hands out, verifies the signature locally with licverify, and
// keeps the verified snapshot. bkn-safe is the only component that talks to the
// outside; every module consumes the text and verifies it for itself.
//
// Behaviour when the hub is unreachable is defined, and neither extreme is
// acceptable: failing open hands paid capability out for free, while failing
// closed immediately turns a momentary hub blip into a customer outage. The
// shipped form therefore serves from the last snapshot that verified, inside
// licverify's grace period, and falls back to community behaviour once the
// grace period is spent. A cold start that never reached the hub has no
// snapshot, which is community behaviour already — the deniedGate zero value.
//
// TODO(context-loader license client): until that client exists this returns
// deniedGate{}, so a production build has every paid feature off. The failure
// direction is correct, but note what it means for release planning: an ee
// binary built without -tags ee_dev cannot serve context_probe at all. What
// merges here is the socket, not a sellable capability — see bkn-docs
// shared/licensing/context-loader-ee-socket.md §3.
func DefaultGate() Gate { return deniedGate{} }
