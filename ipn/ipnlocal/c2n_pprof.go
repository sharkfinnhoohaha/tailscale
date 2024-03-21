// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build !js && !wasm

package ipnlocal

import (
	"fmt"
	"net/http"
	"runtime/pprof"
)

func init() {
	c2nLogHeap = func(w http.ResponseWriter, r *http.Request) {
		pprof.WriteHeapProfile(w)
	}

	c2nPprof = func(w http.ResponseWriter, r *http.Request, profile string) {
		p := pprof.Lookup(profile)
		if p == nil {
			http.Error(w, fmt.Sprintf("unsupported profile %s", profile), http.StatusBadRequest)
		}
		p.WriteTo(w, 0)
	}
}
