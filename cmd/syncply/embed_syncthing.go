//go:build embedsyncthing

package main

import _ "embed"

// Compilación de distribución para Windows (make dist-windows): Syncthing
// e as licenzas van dentro de syncply.exe, que se instala a si mesmo.

//go:embed embed/syncthing.exe.gz
var embeddedSyncthingGz []byte

//go:embed embed/LICENZAS.txt
var embeddedLicenses []byte
