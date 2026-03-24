<p align="center">
  <strong>CODONG</strong><br>
  Первый в мире AI-нативный язык программирования
</p>

<p align="center">
  <a href="https://codong.org">Сайт</a> |
  <a href="https://codong.org/arena/">Arena</a> |
  <a href="../SPEC.md">Спецификация</a> |
  <a href="../WHITEPAPER.md">Белая книга</a> |
  <a href="../README.md">English</a>
</p>

<p align="center">
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
  <img src="https://img.shields.io/badge/language-Codong-orange.svg" alt="Language: Codong">
  <a href="https://codong.org/arena/"><img src="https://img.shields.io/badge/arena-live-purple.svg" alt="Arena: Live"></a>
</p>

---

## Бенчмарк Arena

Когда AI-модель пишет одно и то же приложение на разных языках, Codong генерирует минимум кода, минимум токенов и делает это быстрее всех.

| Метрика | Codong | Python | JavaScript | Go | Java |
|---------|--------|--------|------------|-----|------|
| Выходные токены | **677** | 1,021 | 992 | 1,929 | 2,653 |
| Строки кода | **65** | 121 | 89 | 193 | 225 |
| Время генерации | **7.9s** | 13.6s | 10.4s | 20.0s | 26.0s |

Тестируйте онлайн: [codong.org/arena](https://codong.org/arena/)

---

## Быстрый старт за 30 секунд

```bash
# 1. Скачать бинарный файл
curl -fsSL https://codong.org/install.sh | sh

# 2. Написать первую программу
echo 'print("Hello, Codong!")' > hello.cod

# 3. Запустить
codong eval hello.cod
```

Web API в пяти строках:

```
web.get("/", fn(req) => web.json({message: "Hello from Codong"}))
web.get("/health", fn(req) => web.json({status: "ok"}))
server = web.serve(port: 8080)
```

---

## Почему Codong

Большинство языков программирования создано для написания людьми и исполнения машинами. Codong создан для написания AI, проверки людьми и исполнения машинами.

### Три ключевых преимущества

**1. Нулевая неоднозначность**: В Python более 5 способов сделать HTTP-запрос. Каждый выбор тратит токены. В Codong для всего есть ровно один способ.

**2. Структурированные ошибки**: Каждая ошибка -- структурированный JSON с полями `code`, `message`, `fix`, `retry`. AI не нужно парсить стек-трейсы.

**3. Встроенные модули**: 8 модулей покрывают 90% задач AI-программирования. Менеджер пакетов не нужен, стоимость выбора нулевая.

---

## Встроенные модули

| Модуль | Назначение |
|--------|-----------|
| `web` | HTTP-сервер, маршрутизация, middleware, WebSocket |
| `db` | PostgreSQL, MySQL, MongoDB, Redis, SQLite |
| `http` | HTTP-клиент |
| `llm` | GPT, Claude, Gemini -- единый интерфейс |
| `fs` | Операции с файловой системой |
| `json` | Обработка JSON |
| `env` | Переменные окружения |
| `time` | Дата, время, длительность |
| `error` | Создание и обработка структурированных ошибок |

---

## Пример кода

```
// API-эндпоинт на основе LLM
web.post("/ask", fn(req) {
    question = req.body.question
    answer = llm.ask(
        model: "gpt-4o",
        prompt: "Answer: {question}",
        format: "json"
    )?
    return web.json(answer)
})

server = web.serve(port: 8080)
```

---

## Ссылки

| Ресурс | Ссылка |
|--------|--------|
| Полный README (англ.) | [README.md](../README.md) |
| Сайт | [codong.org](https://codong.org) |
| Arena | [codong.org/arena](https://codong.org/arena/) |
| GitHub | [github.com/brettinhere/Codong](https://github.com/brettinhere/Codong) |

---

MIT -- [LICENSE](../LICENSE)

CODONG -- codong.org
