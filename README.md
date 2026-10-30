# Vice Dolls Helper

Настольное приложение-библиотека сообщений для работы с фан-страницей (Windows, с версией для Mac). Хранит и ротирует готовые тексты (рассылки, сторис, приоритетные сообщения, возвраты, сценарии), быстрая вставка по горячей клавише, вкладка зарплаты моделей, облачный вход и зашифрованный бэкап/перенос между Windows и Mac.

## Сборка (Windows, с Linux/Mac через кросс-компиляцию Go)

```sh
# 1. вспомогательное окно WebView2 (встраивается в основной exe)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o vdhwindow.exe ./winwindow
# 2. основное приложение
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o "Vice Dolls Helper.exe" .
```

## Релизы

Публикуются автоматически: при пуше тега вида `v3.8` GitHub Actions
(`.github/workflows/release.yml`) собирает Windows-сборку и выкладывает
Release с `.exe` (его подхватывает встроенное авто-обновление) и `.zip`.
