#!/bin/sh
# Calcola la prossima versione leggendo i conventional commits fatti dall'ultimo
# tag, e la stampa su stdout. Non stampa niente se non c'è nulla da rilasciare:
# chi lo chiama interpreta l'output vuoto come "non creare nessun tag".
#
# Regole di incremento:
#   '!' prima dei due punti, o 'BREAKING CHANGE:' nel corpo  ->  major
#   feat                                                     ->  minor
#   fix, perf                                                ->  patch
#   tutto il resto (chore, docs, refactor, e i commit che non
#   seguono la convenzione)                                  ->  $BUMP_PREDEFINITO
#
# BUMP_PREDEFINITO=patch fa sì che ogni push sul branch principale produca
# comunque un'immagine. Mettilo a 'none' per rilasciare solo su feat e fix.
#
# I tag non hanno prefisso 'v', come negli altri progetti.
set -eu

BUMP_PREDEFINITO="${BUMP_PREDEFINITO:-patch}"

SEMVER='^[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*$'

# --sort ordina per versione, poi il grep scarta i tag che non sono versioni
# (l'ordine resta quello del sort, quindi il primo è il più recente).
ultimo=$(git tag -l --sort=-v:refname | grep -E "$SEMVER" | head -n1 || true)

if [ -n "$ultimo" ]; then
    base="$ultimo"
    intervallo="${ultimo}..HEAD"
else
    base="0.0.0"
    intervallo="HEAD"
fi

soggetti=$(git log --format='%s' "$intervallo")
corpi=$(git log --format='%b' "$intervallo")

# Nessun commit nuovo: non c'è proprio niente da rilasciare.
[ -n "$soggetti" ] || exit 0

bump="$BUMP_PREDEFINITO"

if echo "$soggetti" | grep -qE '^[a-zA-Z]+(\([^)]*\))?!:' \
|| echo "$corpi"    | grep -qE '^BREAKING[ -]CHANGE:'; then
    bump=major
elif echo "$soggetti" | grep -qE '^feat(\([^)]*\))?:'; then
    bump=minor
elif echo "$soggetti" | grep -qE '^(fix|perf)(\([^)]*\))?:'; then
    bump=patch
fi

# Solo commit di servizio e nessun incremento predefinito: niente rilascio.
[ "$bump" != "none" ] || exit 0

maj=$(echo "$base" | cut -d. -f1)
min=$(echo "$base" | cut -d. -f2)
pat=$(echo "$base" | cut -d. -f3)

case "$bump" in
    major) maj=$((maj + 1)); min=0;             pat=0 ;;
    minor)                   min=$((min + 1));  pat=0 ;;
    patch)                                      pat=$((pat + 1)) ;;
esac

echo "${maj}.${min}.${pat}"
