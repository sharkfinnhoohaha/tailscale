// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package ipnlocal

import (
	"maps"

	"tailscale.com/ipn"
)

func (b *LocalBackend) UpdateOutgoingFiles(updates map[string]ipn.OutgoingFile) {
	b.mu.Lock()
	if b.outgoingFiles == nil {
		b.outgoingFiles = make(map[string]*ipn.OutgoingFile, len(updates))
	}
	for id, file := range updates {
		b.outgoingFiles[id] = &file
	}
	outgoingFiles := maps.Clone(b.outgoingFiles)
	b.mu.Unlock()
	b.send(ipn.Notify{OutgoingFiles: outgoingFiles})
}
