<p align="center">
  <strong>CODONG</strong><br>
  世界首个 AI 原生编程语言
</p>

<p align="center">
  <a href="https://codong.org">官网</a> |
  <a href="https://codong.org/arena/">Arena 竞技场</a> |
  <a href="../SPEC.md">语言规范</a> |
  <a href="../WHITEPAPER.md">白皮书</a> |
  <a href="../README.md">English</a>
</p>

<p align="center">
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
  <img src="https://img.shields.io/badge/language-Codong-orange.svg" alt="Language: Codong">
  <a href="https://codong.org/arena/"><img src="https://img.shields.io/badge/arena-live-purple.svg" alt="Arena: Live"></a>
</p>

---

## Arena 基准测试

当 AI 模型用不同语言编写同一应用时，Codong 产生的代码量最少、Token 消耗最低、生成速度最快。

| 指标 | Codong | Python | JavaScript | Go | Java |
|------|--------|--------|------------|-----|------|
| 输出 Token | **677** | 1,021 | 992 | 1,929 | 2,653 |
| 代码行数 | **65** | 121 | 89 | 193 | 225 |
| 生成耗时 | **7.9s** | 13.6s | 10.4s | 20.0s | 26.0s |

在线测试: [codong.org/arena](https://codong.org/arena/)

---

## 30 秒快速上手

```bash
# 1. 下载二进制文件
curl -fsSL https://codong.org/install.sh | sh

# 2. 编写第一个程序
echo 'print("Hello, Codong!")' > hello.cod

# 3. 运行
codong eval hello.cod
```

五行代码写一个 Web API:

```
web.get("/", fn(req) => web.json({message: "Hello from Codong"}))
web.get("/health", fn(req) => web.json({status: "ok"}))
server = web.serve(port: 8080)
```

---

## 为什么选择 Codong

大多数编程语言为人类编写、机器执行而设计。Codong 为 AI 编写、人类审查、机器执行而设计。

### 三大核心优势

**1. 零歧义**: Python 有 5 种以上 HTTP 请求方式。每次选择都消耗 Token。Codong 每件事只有一种写法。

**2. 结构化错误**: 每个错误都是结构化 JSON，包含 `code`、`message`、`fix`、`retry` 字段。AI 无需解析堆栈跟踪。

**3. 内置模块**: 8 大模块覆盖 90% AI 编程场景。无包管理器，零选择成本。

### Token 经济学

| 场景 | Python/JS | Codong | 节省 |
|------|-----------|--------|------|
| 选择 HTTP 框架 | ~300 token | 0 | 100% |
| 选择数据库 ORM | ~400 token | 0 | 100% |
| 解析错误信息 | ~500 token | ~50 token | 90% |
| 解决包版本冲突 | ~800 token | 0 | 100% |
| **总计** | **~2,800** | **~850** | **~70%** |

---

## 语言特性

- 23 个关键字 (Python 35 个, JavaScript 64 个, Java 67 个)
- 6 种基础类型: string, number, bool, null, list, map
- `{expr}` 字符串插值 -- 不需要反引号、f-string 或 `${}`
- `?` 操作符自动传播错误 -- 不需要 `if err != nil`
- `try/catch` 捕获结构化错误对象

---

## 内置模块

| 模块 | 用途 |
|------|------|
| `web` | HTTP 服务器、路由、中间件、WebSocket |
| `db` | PostgreSQL、MySQL、MongoDB、Redis、SQLite |
| `http` | HTTP 客户端 |
| `llm` | GPT、Claude、Gemini 统一接口 |
| `fs` | 文件系统操作 |
| `json` | JSON 处理 |
| `env` | 环境变量 |
| `time` | 日期、时间、持续时间 |
| `error` | 结构化错误创建和处理 |

---

## 代码示例

```
// LLM 驱动的 API 端点
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

## 链接

| 资源 | 链接 |
|------|------|
| 完整英文 README | [README.md](../README.md) |
| 官网 | [codong.org](https://codong.org) |
| Arena | [codong.org/arena](https://codong.org/arena/) |
| GitHub | [github.com/brettinhere/Codong](https://github.com/brettinhere/Codong) |

---

MIT -- [LICENSE](../LICENSE)

CODONG -- codong.org
