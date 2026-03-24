<p align="center">
  <strong>CODONG</strong><br>
  세계 최초의 AI 네이티브 프로그래밍 언어
</p>

<p align="center">
  <a href="https://codong.org">웹사이트</a> |
  <a href="https://codong.org/arena/">Arena</a> |
  <a href="../SPEC.md">언어 사양</a> |
  <a href="../WHITEPAPER.md">백서</a> |
  <a href="../README.md">English</a>
</p>

<p align="center">
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
  <img src="https://img.shields.io/badge/language-Codong-orange.svg" alt="Language: Codong">
  <a href="https://codong.org/arena/"><img src="https://img.shields.io/badge/arena-live-purple.svg" alt="Arena: Live"></a>
</p>

---

## Arena 벤치마크

AI 모델이 동일한 애플리케이션을 서로 다른 언어로 작성할 때, Codong은 가장 적은 코드, 가장 적은 토큰, 가장 빠른 생성 속도를 달성합니다.

| 지표 | Codong | Python | JavaScript | Go | Java |
|------|--------|--------|------------|-----|------|
| 출력 토큰 | **677** | 1,021 | 992 | 1,929 | 2,653 |
| 코드 라인 수 | **65** | 121 | 89 | 193 | 225 |
| 생성 시간 | **7.9s** | 13.6s | 10.4s | 20.0s | 26.0s |

라이브 테스트: [codong.org/arena](https://codong.org/arena/)

---

## 30초 퀵스타트

```bash
# 1. 바이너리 다운로드
curl -fsSL https://codong.org/install.sh | sh

# 2. 첫 번째 프로그램 작성
echo 'print("Hello, Codong!")' > hello.cod

# 3. 실행
codong eval hello.cod
```

5줄로 Web API 만들기:

```
web.get("/", fn(req) => web.json({message: "Hello from Codong"}))
web.get("/health", fn(req) => web.json({status: "ok"}))
server = web.serve(port: 8080)
```

---

## Codong을 선택하는 이유

대부분의 프로그래밍 언어는 인간이 작성하고 기계가 실행하도록 설계되었습니다. Codong은 AI가 작성하고, 인간이 검토하고, 기계가 실행하도록 설계되었습니다.

### 3가지 핵심 장점

**1. 제로 모호성**: Python에는 HTTP 요청 방법이 5가지 이상 있습니다. 선택할 때마다 토큰이 소모됩니다. Codong은 모든 것에 대해 단 하나의 방법만 있습니다.

**2. 구조화된 에러**: 모든 에러는 `code`, `message`, `fix`, `retry` 필드를 가진 구조화된 JSON입니다. AI가 스택 트레이스를 파싱할 필요가 없습니다.

**3. 내장 모듈**: 8개 모듈이 AI 프로그래밍의 90%를 커버합니다. 패키지 매니저 불필요, 선택 비용 제로.

---

## 내장 모듈

| 모듈 | 용도 |
|------|------|
| `web` | HTTP 서버, 라우팅, 미들웨어, WebSocket |
| `db` | PostgreSQL, MySQL, MongoDB, Redis, SQLite |
| `http` | HTTP 클라이언트 |
| `llm` | GPT, Claude, Gemini 통합 인터페이스 |
| `fs` | 파일 시스템 작업 |
| `json` | JSON 처리 |
| `env` | 환경 변수 |
| `time` | 날짜, 시간, 기간 |
| `error` | 구조화된 에러 생성 및 처리 |

---

## 코드 예제

```
// LLM 기반 API 엔드포인트
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

## 링크

| 리소스 | 링크 |
|--------|------|
| 전체 영문 README | [README.md](../README.md) |
| 웹사이트 | [codong.org](https://codong.org) |
| Arena | [codong.org/arena](https://codong.org/arena/) |
| GitHub | [github.com/brettinhere/Codong](https://github.com/brettinhere/Codong) |

---

MIT -- [LICENSE](../LICENSE)

CODONG -- codong.org
