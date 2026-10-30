// Package cloudflare embeds the exact Worker source used by the mail module.
package cloudflare

import _ "embed"

//go:embed worker.mjs
var worker string

//go:embed notification.mjs
var notification string

func WorkerSource() string       { return worker }
func NotificationSource() string { return notification }
