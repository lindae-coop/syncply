#!/bin/sh
# Xera o ficheiro de licenzas dun binario de Syncply: a cabeceira indicada,
# as licenzas de todo o código de terceiros compilado dentro (Go e os
# módulos que enlaza o paquete, segundo `go list -deps`) e o texto completo
# da AGPL-3.0 (LICENSE). Así a lista segue ao día ao cambiar dependencias.
#
# Uso (desde a raíz do repositorio, con Go e os módulos descargados):
#
#   packaging/licenzas.sh CABECEIRA PAQUETE GOOS... > LICENZAS.txt
#
# Exemplo: packaging/licenzas.sh packaging/linux/LICENZAS.txt ./cmd/syncply linux
#
# Falla se algún módulo non ten ficheiro de licenza.
set -eu

[ $# -ge 3 ] || { echo "uso: $0 CABECEIRA PAQUETE GOOS..." >&2; exit 2; }
cabeceira=$1
pkg=$2
shift 2

mods=$(for goos in "$@"; do
	GOOS=$goos go list -deps \
		-f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' "$pkg"
done | sort -u)

cat "$cabeceira"
printf '\n\n======================================================================\n'
printf 'Licenzas do código de terceiros compilado dentro\n'
printf '======================================================================\n'

printf '\n--- Go (biblioteca estándar e runtime) ---\n\n'
cat "$(go env GOROOT)/LICENSE"

echo "$mods" | while read -r path ver dir; do
	[ -n "$path" ] || continue
	printf '\n--- %s %s ---\n' "$path" "$ver"
	n=0
	for f in "$dir"/LICENSE* "$dir"/LICENCE* "$dir"/COPYING* "$dir"/NOTICE*; do
		[ -f "$f" ] || continue
		printf '\n[%s]\n\n' "$(basename "$f")"
		cat "$f"
		n=$((n + 1))
	done
	if [ "$n" -eq 0 ]; then
		echo "licenzas.sh: $path $ver non ten ficheiro de licenza" >&2
		exit 1
	fi
done

printf '\n\n======================================================================\n'
printf 'Syncply: GNU Affero General Public License, versión 3\n'
printf '======================================================================\n\n'
cat LICENSE
