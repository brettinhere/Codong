<p align="center">
  <strong>CODONG</strong><br>
  Die weltweit erste AI-native Programmiersprache
</p>

<p align="center">
  <a href="https://codong.org">Webseite</a> |
  <a href="https://codong.org/arena/">Arena</a> |
  <a href="../SPEC.md">Spezifikation</a> |
  <a href="../WHITEPAPER.md">Whitepaper</a> |
  <a href="../README.md">English</a>
</p>

<p align="center">
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
  <img src="https://img.shields.io/badge/language-Codong-orange.svg" alt="Language: Codong">
  <a href="https://codong.org/arena/"><img src="https://img.shields.io/badge/arena-live-purple.svg" alt="Arena: Live"></a>
</p>

---

## Arena-Benchmark

Wenn ein AI-Modell dieselbe Anwendung in verschiedenen Sprachen schreibt, erzeugt Codong den wenigsten Code, die wenigsten Tokens und ist am schnellsten.

| Metrik | Codong | Python | JavaScript | Go | Java |
|--------|--------|--------|------------|-----|------|
| Ausgabe-Tokens | **677** | 1,021 | 992 | 1,929 | 2,653 |
| Codezeilen | **65** | 121 | 89 | 193 | 225 |
| Generierungszeit | **7.9s** | 13.6s | 10.4s | 20.0s | 26.0s |

Live testen: [codong.org/arena](https://codong.org/arena/)

---

## Schnellstart in 30 Sekunden

```bash
# 1. Binary herunterladen
curl -fsSL https://codong.org/install.sh | sh

# 2. Erstes Programm schreiben
echo 'print("Hello, Codong!")' > hello.cod

# 3. Ausfuehren
codong eval hello.cod
```

Web-API in fuenf Zeilen:

```
web.get("/", fn(req) => web.json({message: "Hello from Codong"}))
web.get("/health", fn(req) => web.json({status: "ok"}))
server = web.serve(port: 8080)
```

---

## Warum Codong

Die meisten Programmiersprachen wurden fuer Menschen zum Schreiben und Maschinen zum Ausfuehren entworfen. Codong wurde fuer AI zum Schreiben, Menschen zum Pruefen und Maschinen zum Ausfuehren entworfen.

### Drei Kernvorteile

**1. Null Mehrdeutigkeit**: Python hat mehr als 5 Wege, einen HTTP-Request zu machen. Jede Entscheidung verbraucht Tokens. Codong hat genau einen Weg fuer alles.

**2. Strukturierte Fehler**: Jeder Fehler ist strukturiertes JSON mit den Feldern `code`, `message`, `fix`, `retry`. AI muss keine Stack-Traces parsen.

**3. Eingebaute Module**: 8 Module decken 90% der AI-Programmieraufgaben ab. Kein Paketmanager noetig, keine Auswahlkosten.

---

## Eingebaute Module

| Modul | Zweck |
|-------|-------|
| `web` | HTTP-Server, Routing, Middleware, WebSocket |
| `db` | PostgreSQL, MySQL, MongoDB, Redis, SQLite |
| `http` | HTTP-Client |
| `llm` | GPT, Claude, Gemini -- einheitliche Schnittstelle |
| `fs` | Dateisystem-Operationen |
| `json` | JSON-Verarbeitung |
| `env` | Umgebungsvariablen |
| `time` | Datum, Zeit, Dauer |
| `error` | Strukturierte Fehlererstellung und -behandlung |

---

## Codebeispiel

```
// LLM-gesteuerter API-Endpunkt
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

## Links

| Ressource | Link |
|-----------|------|
| Vollstaendiges README (Englisch) | [README.md](../README.md) |
| Webseite | [codong.org](https://codong.org) |
| Arena | [codong.org/arena](https://codong.org/arena/) |
| GitHub | [github.com/brettinhere/Codong](https://github.com/brettinhere/Codong) |

---

MIT -- [LICENSE](../LICENSE)

CODONG -- codong.org
