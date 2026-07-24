// Go Map Lab — локальное учебное веб-приложение о внутреннем устройстве map в Go.
//
// В исполняемом пакете почти нет логики предметной области. Функция main только
// читает настройки процесса, собирает HTTP-сервер и запускает прослушивание порта.
// Алгоритмы map находятся в internal/lab, unsafe-инспекция памяти — в
// internal/inspector, а HTTP-маршрутизация — в internal/server.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"go-map-internals-lab/internal/server"
)

// ServerAddress — локальный TCP-адрес, на котором работает http.Server.
// Это смысловой псевдоним: базовым типом по-прежнему остаётся обычная строка.
type ServerAddress = string

// HeaderReadTimeout — максимальное время, которое клиент может потратить на
// отправку HTTP-заголовков.
type HeaderReadTimeout = time.Duration

const defaultHeaderReadTimeout HeaderReadTimeout = 5 * time.Second

func main() {
	// По умолчанию сервер привязывается к loopback-интерфейсу. Лаборатория
	// предназначена для запуска на том же компьютере, а не для публикации как
	// производственный веб-сервис.
	address := flag.String(
		"addr",
		"127.0.0.1:8080",
		"адрес локального веб-интерфейса",
	)
	flag.Parse()

	// server.New возвращает http.Handler, но сам не открывает сетевой порт.
	// Благодаря этому настройки процесса остаются в main, а приложение можно
	// независимо проверять через httptest в пакете internal/server.
	handler := server.New()
	httpServer := &http.Server{
		Addr:              ServerAddress(*address),
		Handler:           handler,
		ReadHeaderTimeout: defaultHeaderReadTimeout,
	}

	fmt.Printf("\nGo Map Lab запущен: http://%s\n", *address)
	fmt.Println("Остановить: Ctrl+C")

	// При штатном завершении ListenAndServe возвращает http.ErrServerClosed.
	// Любая другая ошибка означает, что сервер не смог продолжить работу.
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
