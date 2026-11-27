//go:build embedsyncthing

package main

import _ "embed"

// Compilación de distribución para Linux (make dist): Syncthing e as
// licenzas van dentro de syncply, que se instala a si mesmo.

//go:embed embed/linux/syncthing.gz
var embeddedSyncthingGz []byte

//go:embed embed/linux/LICENZAS.txt
var embeddedLicenses []byte
