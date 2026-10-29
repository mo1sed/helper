#!/bin/bash
cd "$(dirname "$0")"
echo "Собираю Vice Dolls Helper — быстрая вставка…"
if ! command -v swiftc >/dev/null 2>&1; then
  echo "Нужны инструменты разработчика Apple. Сейчас macOS предложит их установить."
  xcode-select --install
  echo "После установки запусти этот файл ещё раз."
  read -n1 -r -p "Нажми любую клавишу, чтобы закрыть."
  exit 0
fi
swiftc -O VDHPaste.swift -o VDHPaste -framework Cocoa -framework Carbon
if [ $? -ne 0 ]; then
  echo "Не собралось. Скопируй текст ошибки выше и отправь разработчику."
  read -n1 -r -p "Нажми любую клавишу, чтобы закрыть."
  exit 1
fi
echo "Готово. Запускаю. Значок VD появится в строке меню вверху справа."
echo "Горячая клавиша вставки: Control + Option + V"
pkill -x VDHPaste 2>/dev/null
nohup ./VDHPaste >/dev/null 2>&1 &
sleep 1
echo "Запущено. Это окно можно закрыть."
