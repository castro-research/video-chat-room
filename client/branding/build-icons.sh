#!/bin/sh
# Gera os ícones de macOS e iOS (client/apple/Resources) a partir do K da Kurz (frontend/public/favicon.svg do projecto web).
# Requer: rsvg-convert (brew install librsvg), iconutil (macOS), magick (brew install imagemagick).
set -e
cd "$(dirname "$0")/.."

# formas em coordenadas 64×64: o K da Kurz livre (sem balão à volta) e um balão de chat pequeno a "sair" dele, com a cauda a apontar para o braço
DEFS='<defs>
<linearGradient id="bg" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#1b242b"/><stop offset="1" stop-color="#0d1115"/></linearGradient>
<linearGradient id="kt" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#7ae0dc"/><stop offset="1" stop-color="#2fa9a5"/></linearGradient>
<linearGradient id="kb" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#1f8f8b"/><stop offset="1" stop-color="#136e6b"/></linearGradient>
<filter id="sh" x="-20%" y="-20%" width="140%" height="140%"><feDropShadow dx="0" dy="6" stdDeviation="10" flood-color="#000" flood-opacity=".4"/></filter></defs>'
K='<rect x="14" y="13" width="10" height="38" rx="2.5" fill="#eef2f5"/><path d="M27 32H38.5L52 51H40.5Z" fill="url(#kb)"/><path d="M27 32L40.5 13H52L38.5 32Z" fill="url(#kt)"/>'
BUBBLE='<path d="M37,5H52A6,6 0 0 1 58,11V14A6,6 0 0 1 52,20H40L33.5,24.5L35,20H37A6,6 0 0 1 31,14V11A6,6 0 0 1 37,5Z" fill="#eef2f5" stroke="#eef2f5" stroke-width="1" stroke-linejoin="round"/><circle cx="38" cy="12.5" r="2" fill="#136e6b"/><circle cx="44.5" cy="12.5" r="2" fill="#1f8f8b"/><circle cx="51" cy="12.5" r="2" fill="#2fa9a5"/>'
# o translate/scale externo centra o conjunto K+balão (x 6–58, y 5–57) com folga para a máscara do iOS e os cantos do azulejo
MARK="<g transform=\"translate(2.56 3.4) scale(0.92)\"><g transform=\"translate(-5.9 13.95) scale(0.85)\">$K</g>$BUBBLE</g>"
OPEN='<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024">'

# iOS: quadrado cheio e opaco (o sistema aplica a máscara)
FULL="$OPEN$DEFS<rect width=\"1024\" height=\"1024\" fill=\"url(#bg)\"/><g transform=\"scale(16)\">$MARK</g></svg>"
# macOS/Linux: o ícone traz a própria forma (azulejo arredondado com margem de 100px, borda fina e sombra suave)
TILE="$OPEN$DEFS<rect x=\"100\" y=\"100\" width=\"824\" height=\"824\" rx=\"193\" fill=\"#000\" filter=\"url(#sh)\"/><g transform=\"translate(100 100) scale(12.875)\"><rect width=\"64\" height=\"64\" rx=\"15\" fill=\"url(#bg)\"/><rect x=\".75\" y=\".75\" width=\"62.5\" height=\"62.5\" rx=\"14.25\" fill=\"none\" stroke=\"#2c3842\" stroke-width=\"1.5\"/>$MARK</g></svg>"

render() { printf '%s' "$1" | rsvg-convert -w "$2" -h "$2" -o "$3"; }

mkdir -p branding
printf '%s' "$FULL" > branding/icon.svg
printf '%s' "$TILE" > branding/icon-tile.svg

# iOS (ícone único 1024, sem canal alfa)
IOS=apple/Resources/iOS/Assets.xcassets
mkdir -p "$IOS/AppIcon.appiconset"
render "$FULL" 1024 "$IOS/AppIcon.appiconset/icon-1024.png"
magick "$IOS/AppIcon.appiconset/icon-1024.png" -alpha off "$IOS/AppIcon.appiconset/icon-1024.png"
echo '{ "info": { "author": "xcode", "version": 1 } }' > "$IOS/Contents.json"
cat > "$IOS/AppIcon.appiconset/Contents.json" <<'EOF'
{
  "images": [ { "filename": "icon-1024.png", "idiom": "universal", "platform": "ios", "size": "1024x1024" } ],
  "info": { "author": "xcode", "version": 1 }
}
EOF

# macOS (catálogo com 16..512 @1x/@2x)
MAC=apple/Resources/macOS/Assets.xcassets
mkdir -p "$MAC/AppIcon.appiconset"
echo '{ "info": { "author": "xcode", "version": 1 } }' > "$MAC/Contents.json"
IMAGES=""
for s in 16 32 128 256 512; do
  render "$TILE" "$s" "$MAC/AppIcon.appiconset/icon_${s}.png"
  render "$TILE" "$((s * 2))" "$MAC/AppIcon.appiconset/icon_${s}@2x.png"
  IMAGES="$IMAGES{ \"filename\": \"icon_${s}.png\", \"idiom\": \"mac\", \"scale\": \"1x\", \"size\": \"${s}x${s}\" }, { \"filename\": \"icon_${s}@2x.png\", \"idiom\": \"mac\", \"scale\": \"2x\", \"size\": \"${s}x${s}\" }, "
done
printf '{ "images": [ %s ], "info": { "author": "xcode", "version": 1 } }\n' "${IMAGES%, }" > "$MAC/AppIcon.appiconset/Contents.json"
echo "ok"
