// Package ops embeds the reviewed shell templates shipped with the helper.
package ops

import "embed"

//go:embed backup-panel.sh templates/*.sh
var Templates embed.FS
