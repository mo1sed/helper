#!/bin/bash
cd "$(dirname "$0")"
APP="Vice Dolls Helper.app"
echo "Собираю Vice Dolls Helper для Mac…"
if ! command -v swiftc >/dev/null 2>&1; then
  echo "Нужны инструменты разработчика Apple — сейчас macOS предложит установить."
  xcode-select --install
  echo "После установки запусти этот файл ещё раз."
  read -n1 -r -p "Нажми любую клавишу, чтобы закрыть."
  exit 0
fi
swiftc -O VDHApp.swift -o VDHApp -framework Cocoa -framework WebKit
if [ $? -ne 0 ]; then
  echo "НЕ СОБРАЛОСЬ. Скопируй красный текст выше и пришли разработчику."
  read -n1 -r -p "Нажми любую клавишу, чтобы закрыть."
  exit 1
fi
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp VDHApp "$APP/Contents/MacOS/"
cp vd-arm64 vd-amd64 "$APP/Contents/MacOS/" 2>/dev/null
cp icon.icns "$APP/Contents/Resources/"
cp Info.plist "$APP/Contents/"
chmod +x "$APP/Contents/MacOS/"*
xattr -cr "$APP" 2>/dev/null
echo ""
echo "Готово! Создано приложение: $APP"
echo "Сейчас откроется папка — перетащи «Vice Dolls Helper» в «Программы»."
open .
