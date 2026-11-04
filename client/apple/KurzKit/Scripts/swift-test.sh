#!/bin/sh
# Roda `swift test`. Só com as Command Line Tools (sem Xcode) o Swift Testing não está no caminho padrão; aponta para ele.
cd "$(dirname "$0")/.." || exit 1
DEV=$(xcode-select -p 2>/dev/null)
FW="$DEV/Library/Developer/Frameworks"
case "$DEV" in
  *CommandLineTools) exec swift test -Xswiftc -F"$FW" -Xlinker -F"$FW" -Xlinker -rpath -Xlinker "$FW" "$@" ;;
  *) exec swift test "$@" ;;
esac
