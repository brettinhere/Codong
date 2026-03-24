<p align="center">
  <strong>CODONG</strong><br>
  世界初のAIネイティブプログラミング言語
</p>

<p align="center">
  <a href="https://codong.org">公式サイト</a> |
  <a href="https://codong.org/arena/">Arena</a> |
  <a href="../SPEC.md">言語仕様</a> |
  <a href="../WHITEPAPER.md">ホワイトペーパー</a> |
  <a href="../README.md">English</a>
</p>

<p align="center">
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
  <img src="https://img.shields.io/badge/language-Codong-orange.svg" alt="Language: Codong">
  <a href="https://codong.org/arena/"><img src="https://img.shields.io/badge/arena-live-purple.svg" alt="Arena: Live"></a>
</p>

---

## Arena ベンチマーク

AIモデルが同じアプリケーションを異なる言語で記述した場合、Codongは最も少ないコード量、最少のトークン消費、最速の生成速度を実現します。

| 指標 | Codong | Python | JavaScript | Go | Java |
|------|--------|--------|------------|-----|------|
| 出力トークン | **677** | 1,021 | 992 | 1,929 | 2,653 |
| コード行数 | **65** | 121 | 89 | 193 | 225 |
| 生成時間 | **7.9s** | 13.6s | 10.4s | 20.0s | 26.0s |

ライブテスト: [codong.org/arena](https://codong.org/arena/)

---

## 30秒クイックスタート

```bash
# 1. バイナリをダウンロード
curl -fsSL https://codong.org/install.sh | sh

# 2. 最初のプログラムを書く
echo 'print("Hello, Codong!")' > hello.cod

# 3. 実行
codong eval hello.cod
```

5行でWeb API:

```
web.get("/", fn(req) => web.json({message: "Hello from Codong"}))
web.get("/health", fn(req) => web.json({status: "ok"}))
server = web.serve(port: 8080)
```

---

## Codongを選ぶ理由

ほとんどのプログラミング言語は人間が書き、機械が実行するために設計されています。CodongはAIが書き、人間がレビューし、機器が実行するために設計されています。

### 3つの核心的優位性

**1. ゼロ曖昧性**: PythonにはHTTPリクエストの方法が5つ以上あります。選択のたびにトークンを消費します。Codongは全てに対して1つの方法しかありません。

**2. 構造化エラー**: 全てのエラーは `code`、`message`、`fix`、`retry` フィールドを持つ構造化JSONです。AIはスタックトレースを解析する必要がありません。

**3. 組み込みモジュール**: 8つのモジュールがAIプログラミングの90%をカバー。パッケージマネージャー不要、選択コストゼロ。

---

## 組み込みモジュール

| モジュール | 用途 |
|-----------|------|
| `web` | HTTPサーバー、ルーティング、ミドルウェア、WebSocket |
| `db` | PostgreSQL、MySQL、MongoDB、Redis、SQLite |
| `http` | HTTPクライアント |
| `llm` | GPT、Claude、Gemini統一インターフェース |
| `fs` | ファイルシステム操作 |
| `json` | JSON処理 |
| `env` | 環境変数 |
| `time` | 日付、時間、期間 |
| `error` | 構造化エラーの作成と処理 |

---

## コード例

```
// LLM駆動のAPIエンドポイント
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

## リンク

| リソース | リンク |
|---------|------|
| 完全な英語README | [README.md](../README.md) |
| 公式サイト | [codong.org](https://codong.org) |
| Arena | [codong.org/arena](https://codong.org/arena/) |
| GitHub | [github.com/brettinhere/Codong](https://github.com/brettinhere/Codong) |

---

MIT -- [LICENSE](../LICENSE)

CODONG -- codong.org
