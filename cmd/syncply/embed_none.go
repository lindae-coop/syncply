//go:build !embedsyncthing || !(windows || linux)

package main

var embeddedSyncthingGz, embeddedLicenses []byte
