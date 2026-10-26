#!/bin/sh
# Gera dist/Kurz-Community.dmg (Release, assinado com a identidade do project.yml) para enviar a outra pessoa.
set -e
cd "$(dirname "$0")"
xcodebuild -workspace Kurz.xcworkspace -scheme KurzMac-Release -configuration Release -derivedDataPath build build | tail -1
APP="build/Build/Products/Release/Kurz Community.app"
mkdir -p dist && rm -f dist/Kurz-Community.dmg
hdiutil create -volname "Kurz Community" -srcfolder "$APP" -ov -format UDZO dist/Kurz-Community.dmg >/dev/null
echo "dist/Kurz-Community.dmg"
