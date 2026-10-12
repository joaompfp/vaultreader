// SPDX-License-Identifier: AGPL-3.0-only

// Package web holds the SPA and bundled frontend libraries, embedded into
// the binary. Paths inside Files keep their "static/" prefix.
package web

import "embed"

//go:embed static
var Files embed.FS
